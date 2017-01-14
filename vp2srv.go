package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"database/sql"

	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/gin-gonic/gin.v1"
)

const (
	format    = "200601021504"
	formatDay = "20060102"
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
	CurrDate                                                                                  string
	WindDir                                                                                   string
	WindSpeed, InTemp, BarTrend, InHum, AvgSpeed, ForecastIcon, OutTemp, DayRain              float64
	StormRain, OutHum, StormStart, RainRate, Bar, MonRain, YearRain, ForecastRule, WindDirDeg float64
}
type vanprod struct {
	db          *sql.DB
	archive     Archive
	archiveDay  Archive
	archiveLast time.Time
	current     Current
	camdata     []byte
}

//{"OutHum":93,"ForecastIcon":6,"YearRain":0.2,"InTemp":"1.0","WindDir":"S","StormStart":0,"WindSpeed":"1.6","RainRate":0,
//"MonRain":0.2,"WindDirDeg":"194","DayRain":0,"InHum":55,"StormRain":0,"BarTrend":20,"CurrDate":"20170112070139","Bar":"1010.9",
//"OutTemp":"-6.8","AvgWspeed":"3.2","ForecastRule":45}

func (vp *vanprod) getArchive(from, to string, format string, interval time.Duration, table string) Archive {
	// first we decide if we want to rebuilt in memory result set in vp.archive
	// - only for specific query(from,to) or if the value of vp.archiveLast does not match last ID in the database
	// - otherwise we return result set direct from memory

	fromTime, _ := time.Parse(format, from)

	toTime, _ := time.Parse(format, to)
	iter := fromTime.Truncate(interval)
	// the instance is shared and we create
	archive := Archive{
		From:     fromTime,
		To:       toTime,
		Start:    iter.Unix() * 1000,
		Interval: int64(interval.Seconds() * 1000),
	}
	stmt, err := vp.db.Prepare("select ID, HI_OUTTEMP, LO_OUTTEMP, RAIN, BAR, HI_WSPEED from " + table + "  where id = ? and hi_outtemp < 100 and rain < 400")
	if err != nil {
		log.Println(err)
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
		// log.Printf("ID=%d\n", id)
		if err != nil {
			// log.Println(err)
			if id > 0 {
				cnt++
			}
		} else {
			cnt = 0
		}
		if cnt > 30 {
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
	return archive
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
			log.Println(err)
		}
		stmt := `insert or replace into data_archive(
			ID, OUTTEMP	, HI_OUTTEMP	, LO_OUTTEMP	, RAIN	, RAIN_RATE	,
			BAR	, OUTHUM	, AVG_WSPEED	, HI_WSPEED	, DIR_HI_WSPEED , AVG_WDIR
			) values(` + strings.Join(record, ",") + ")"
		_, err = vp.db.Exec(stmt)
		if err != nil {
			log.Println(err)
		}

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
		log.Println(err)
	}
	vp.reloadData()
}

func (vp *vanprod) reloadData() {
	vp.archiveLast = vp.getArchiveLast()
	vp.archive = vp.getArchive(
		time.Now().Add(-7*24*time.Hour).Format(format),
		time.Now().Format(format),
		format,
		time.Minute*30,
		"data_archive")
	vp.archiveDay = vp.getArchive(
		"20120101",
		time.Now().Format(formatDay),
		formatDay,
		time.Hour*24,
		"data_archive_day")
	// nekter udaje nebudeme predavata, proto vynulujeme
	vp.archiveDay.Bar = nil
	vp.archiveDay.HiWSpeed = nil
	//tx.Commit()
}
func main() {

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
	vp.reloadData()
	// Group using gin.BasicAuth() middleware
	// gin.Accounts is a shortcut for map[string]string
	gin.SetMode("debug")
	router := gin.Default()
	admin := router.Group("/admin/", gin.BasicAuth(gin.Accounts{
		"pozdechov": "vp2",
	}))
	router.StaticFS("/static", http.Dir("www"))
	router.StaticFS("/Bacovi-rodokmen", http.Dir("www/Bacovi-rodokmen"))
	router.LoadHTMLFiles("www/vp2.html")
	router.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "vp2.html", nil)
	})
	router.GET("/archive", func(c *gin.Context) {
		if c.Query("from")+c.Query("to") == "" {
			c.JSON(http.StatusOK, vp.archive)
		} else {
			archive := vp.getArchive(
				c.DefaultQuery("from", time.Now().Add(-7*24*time.Hour).Format(format)),
				c.DefaultQuery("to", time.Now().Format(format)),
				format,
				time.Minute*30,
				"data_archive")

			c.JSON(http.StatusOK, archive)
		}
	})
	router.GET("/archive/day", func(c *gin.Context) {
		c.JSON(http.StatusOK, vp.archiveDay)
	})
	router.GET("/archive/lastid", vp.getArchiveLastID)
	router.GET("/current", func(c *gin.Context) {
		c.JSON(http.StatusOK, vp.current)
	})
	router.GET("/camview.jpg", func(c *gin.Context) {
		c.Data(http.StatusOK, c.ContentType(), vp.camdata)
	})
	router.GET("/vp2_json", func(c *gin.Context) {
		d := vp.current
		t := d.CurrDate
		c.String(http.StatusOK, "|%s|%s|%.1f|%.0f|%.1f|%.1f|%.0f|%.1f|",
			t[6:8]+"."+t[4:6]+"."+t[0:4], t[8:10]+":"+t[10:12],
			d.OutTemp, d.OutHum, d.Bar, d.AvgSpeed/3.6, d.WindDirDeg, d.DayRain)
	})
	admin.POST("/submit/archive", vp.postArchive)
	admin.POST("/submit/current", func(c *gin.Context) {
		var json Current
		err := c.BindJSON(&json)
		if err == nil {
			vp.current = json
		} else {
			log.Println(err)
		}
	})
	admin.POST("/submit/camdata", func(c *gin.Context) {
		camdata, err := ioutil.ReadAll(c.Request.Body)
		c.Request.Body.Close()
		if err != nil {
			log.Println(err)
		} else {
			vp.camdata = camdata
		}
	})
	router.Run(":80")
}
