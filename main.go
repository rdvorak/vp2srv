package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/boltdb/bolt"
	"gopkg.in/gin-gonic/gin.v1"
)

//Archive - message with archive data
type Archive struct {
	Start     int64
	Interval  float64
	HiOutTemp []float64
	LoOutTemp []float64
	Rain      []float64
	Bar       []float64
	HiWSpeed  []float64
}
type vanprod struct {
	db      *bolt.DB
	buckets []string
	bucket  map[string]*bolt.Bucket
	archive Archive
}

func (vp *vanprod) createBuckets() {
	vp.buckets = []string{"ID", "OUTTEMP", "HI_OUTTEMP", "LO_OUTTEMP", "RAIN", "RAIN_RATE", "BAR", "OUTHUM", "AVG_WSPEED", "HI_WSPEED", "DIR_HI_WSPEED", "AVG_WDIR"}
	vp.bucket = make(map[string]*bolt.Bucket)
	err := vp.db.Update(func(tx *bolt.Tx) error {
		for _, v := range vp.buckets {
			_, err := tx.CreateBucketIfNotExists([]byte(v))
			if err != nil {
				return fmt.Errorf("create bucket: %s", err)
			}
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}
func (vp *vanprod) getArchive(c *gin.Context) {
	interval := time.Minute * 30
	format := "2006-01-02T15:04:05Z"
	iter := time.Now().Add(-3 * 24 * time.Hour).Truncate(interval)
	vp.archive = Archive{
		Start:    iter.Unix(),
		Interval: interval.Seconds() * 1000,
	}
	vp.db.View(func(tx *bolt.Tx) error {
		for ; time.Now().After(iter); iter = iter.Add(interval) {
			// Assume our events bucket exists and has RFC3339 encoded time keys.
			for _, b := range []string{"HI_OUTTEMP", "LO_OUTTEMP", "RAIN", "BAR", "HI_WSPEED"} {
				v := tx.Bucket([]byte(b)).Get([]byte(iter.Format(format)))
				s, err := strconv.ParseFloat(string(v), 64)
				if v != nil && err != nil {
					log.Println(err)
				}
				if v == nil {
					s = nil
				}
				switch b {
				case "HI_OUTTEMP":
					vp.archive.HiOutTemp = append(vp.archive.HiOutTemp, s)
				case "LO_OUTTEMP":
					vp.archive.LoOutTemp = append(vp.archive.LoOutTemp, s)
				case "RAIN":
					vp.archive.Rain = append(vp.archive.Rain, s)
				case "BAR":
					vp.archive.Bar = append(vp.archive.Bar, s)
				case "HI_WSPEED":
					vp.archive.HiWSpeed = append(vp.archive.HiWSpeed, s)
				}

			}
		}

		return nil
	})
	c.JSON(http.StatusOK, vp.archive)

}
func (vp *vanprod) postArchive(c *gin.Context) {
	defer c.Request.Body.Close()
	//fmt.Println(ioutil.ReadAll(c.Request.Body))
	csvr := csv.NewReader(c.Request.Body)
	err := vp.db.Update(func(tx *bolt.Tx) error {
		for {
			record, err := csvr.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Fatal(err)
			}

			for i, v := range record[0:10] {
				b := tx.Bucket([]byte(vp.buckets[i]))
				err = b.Put([]byte(record[0]), []byte(v))
				if err != nil {
					log.Fatal(err)
				}
			}
			fmt.Println(record)
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}
func main() {
	router := gin.Default()
	router.LoadHTMLGlob("tmpl/*")
	router.GET("/index", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})
	var vp vanprod
	db, err := bolt.Open("vp2.db", 0600, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	vp.db = db
	vp.createBuckets()
	// Group using gin.BasicAuth() middleware
	// gin.Accounts is a shortcut for map[string]string
	admin := router.Group("/admin/", gin.BasicAuth(gin.Accounts{
		"pozdechov": "vp2",
	}))

	router.GET("/archive", vp.getArchive)
	admin.POST("/submit/archive", vp.postArchive)
	router.Run(":8080")
}
