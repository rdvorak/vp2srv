package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"

	"github.com/boltdb/bolt"
	"gopkg.in/gin-gonic/gin.v1"
)

type vanprod struct {
	db      *bolt.DB
	buckets []string
	bucket  map[string]*bolt.Bucket
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
func (vp *vanprod) saveArchiveData(c *gin.Context) {
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

	admin.POST("/submit/archive", vp.saveArchiveData)
	router.Run(":8080")
}
