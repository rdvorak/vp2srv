package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"database/sql"

	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/gin-gonic/gin.v1"
)

const (
	format = "200601021504"
)

//Archive - message with archive data
type Archive struct {
	From      time.Time
	To        time.Time
	Start     int64
	Interval  int64
	HiOutTemp []float64
	LoOutTemp []float64
	Rain      []float64
	Bar       []float64
	HiWSpeed  []float64
}

//Current record
type Current struct {
	CurrDate                                                                                  int
	WindDir                                                                                   string
	WindSpeed, InTemp, BarTrend, InHum, AvgSpeed, Forecasticon, OutTemp, DayRain              float64
	StormRain, OutHum, StormStart, RainRate, Bar, MonRain, YearRain, ForecastRule, WindDirDeg float64
}
type vanprod struct {
	db          *sql.DB
	archive     Archive
	archiveDay  Archive
	archiveLast time.Time
	current     Current
}

//avg_wspeed,bar,bar_trend,curr_date,dayrain,forecasticon,forecastrule,inhum,intemp,monrain,outhum,outtemp,rain_rate,stormrain,stormstart,winddir,winddir_deg,windspeed,yearrain
//9.7,1027.2,236,20170109153242,0,6,5,55,7.2,0.2,78,-6.9,0,0,0,N,15,4.8,0.2

func (vp *vanprod) getArchive(c *gin.Context, format string, interval time.Duration, table string) {
	// first we decide if we want to rebuilt in memory result set in vp.archive
	// - only for specific query(from,to) or if the value of vp.archiveLast does not match last ID in the database
	// - otherwise we return result set direct from memory

	if c.Query("from")+c.Query("to") != "" || !vp.archive.To.Equal(vp.archiveLast) {
		from := c.DefaultQuery("from", time.Now().Add(-7*24*time.Hour).Format(format))
		to := c.DefaultQuery("to", time.Now().Format(format))
		fromTime, _ := time.Parse(format, from)

		toTime, _ := time.Parse(format, to)
		interval := time.Minute * 30
		iter := fromTime.Truncate(interval)
		// the instance is shared and we create
		archive := Archive{
			From:     fromTime,
			To:       toTime,
			Start:    iter.Unix(),
			Interval: int64(interval.Seconds() * 1000),
		}
		stmt, err := vp.db.Prepare("select ID, HI_OUTTEMP, LO_OUTTEMP, RAIN, BAR, HI_WSPEED from " + table + "  where id = ?")
		if err != nil {
			log.Fatal(err)
		}
		defer stmt.Close()
		var id, cnt int
		var hiOutTemp, loOutTemp, rain, bar, hiWspeed float64
		// we will be iterating over time period in interval of 30 minutes
		// and we expect the DB has record for each interval; if record is missing, the previous values are used up to 5 missing intervals
		// time range is also limited by the last recorded interval and current time

		// we added 1 second only for comparison, otherwise we should also test time.Equal
		for ; toTime.Add(time.Second).After(iter) && vp.archiveLast.Add(time.Second).After(iter); iter = iter.Add(interval) {

			err = stmt.QueryRow(iter.Format(format)).Scan(&id, &hiOutTemp, &loOutTemp, &rain, &bar, &hiWspeed)
			log.Printf("ID=%d\n", id)
			if err != nil {
				log.Println(err)
				if id > 0 {
					cnt++
				}
			} else {
				cnt = 0
			}
			if cnt > 5 {
				log.Println("Gap in DB is too big")
				break
			}
			// for missing intervals in DB, we fill the gap with last values
			if id > 0 {
				archive.HiOutTemp = append(archive.HiOutTemp, hiOutTemp)
				archive.LoOutTemp = append(archive.LoOutTemp, loOutTemp)
				archive.Rain = append(archive.Rain, rain)
				archive.Bar = append(archive.Bar, bar)
				archive.HiWSpeed = append(archive.HiWSpeed, hiWspeed)
			}
		}
		// we update  in memory result set only in case of default query
		if c.Query("from")+c.Query("to") == "" {
			vp.archive = archive
		}
		c.JSON(http.StatusOK, archive)
	} else {
		c.JSON(http.StatusOK, vp.archive)
	}
}

func (vp *vanprod) getArchiveLast() time.Time {

	var id string
	err := vp.db.QueryRow("select max(ID) max_id from data_archive").Scan(&id)
	if err != nil {
		log.Println(err)
	}
	archiveLast, _ := time.Parse(format, id)
	return archiveLast
}

func (vp *vanprod) getArchiveLastID(c *gin.Context) {
	c.String(http.StatusOK, vp.archiveLast.Format(format))
}

func (vp *vanprod) postArchive(c *gin.Context) {
	defer c.Request.Body.Close()
	//fmt.Println(ioutil.ReadAll(c.Request.Body))
	csvr := csv.NewReader(c.Request.Body)
	//tx, err := vp.db.Begin()
	for {
		record, err := csvr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		stmt := `insert or replace into data_archive(
			ID, OUTTEMP	, HI_OUTTEMP	, LO_OUTTEMP	, RAIN	, RAIN_RATE	,
			BAR	, OUTHUM	, AVG_WSPEED	, HI_WSPEED	, DIR_HI_WSPEED , AVG_WDIR
			) values(` + strings.Join(record, ",") + ")"
		_, err = vp.db.Exec(stmt)
		if err != nil {
			log.Fatal(err)
		}
		vp.archiveLast = vp.getArchiveLast()

		fmt.Println(record)
	}
	stmt := `delete from data_archive_day;
	insert or replace into data_archive_day(
		ID, HI_OUTTEMP	, LO_OUTTEMP	, RAIN	, RAIN_RATE	,
		BAR	, OUTHUM	, AVG_WSPEED	, HI_WSPEED
		) select substr(id,1,8), max(hi_outtemp), min(lo_outtemp),
		max(rain_rate), sum(rain), round(avg(bar),2), round(avg(outhum),2), round(avg(avg_wspeed),2), max(hi_wspeed)
		from data_archive
		group by substr(id,1,8)
		;
		`
	_, err := vp.db.Exec(stmt)
	if err != nil {
		log.Fatal(err)
	}
	//tx.Commit()
}
func main() {
	router := gin.Default()
	router.LoadHTMLGlob("tmpl/*")
	router.GET("/index", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})
	var vp vanprod
	db, err := sql.Open("sqlite3", "./vp2.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	sqlStmt := `
create table if not exists data_archive (
ID integer not null primary key,
OUTTEMP	float, HI_OUTTEMP	float, LO_OUTTEMP	float, RAIN	float, RAIN_RATE	float,
BAR	float, OUTHUM	float, AVG_WSPEED	float, HI_WSPEED	float, DIR_HI_WSPEED text,
AVG_WDIR	text
);
create table if not exists data_archive_day (
ID integer not null primary key,
OUTTEMP	float, HI_OUTTEMP	float, LO_OUTTEMP	float, RAIN	float, RAIN_RATE	float,
BAR	float, OUTHUM	float, AVG_WSPEED	float, HI_WSPEED	float
AVG_WDIR	text
);
`

	_, err = db.Exec(sqlStmt)
	if err != nil {
		log.Printf("%q: %s\n", err, sqlStmt)
		return
	}
	vp.db = db
	// Group using gin.BasicAuth() middleware
	// gin.Accounts is a shortcut for map[string]string
	admin := router.Group("/admin/", gin.BasicAuth(gin.Accounts{
		"pozdechov": "vp2",
	}))

	router.GET("/archive", func(c *gin.Context) { vp.getArchive(c, "200601021504", time.Minute*30, "data_archive") })
	router.GET("/archive/day", func(c *gin.Context) { vp.getArchive(c, "20060102", time.Hour*24, "data_archive_day") })
	router.GET("/archive/lastid", vp.getArchiveLastID)
	admin.POST("/submit/archive", vp.postArchive)
	router.Run(":8080")
}
