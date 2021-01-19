package main

import (
	"database/sql"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type GeneratorOptions struct {
	size        string
	host        string
	port        int
	user        string
	password    string
	concurrency int
	dbName      string
	unit        string
}

var opt = GeneratorOptions{
	unit: "KB",
}

func main() {
	flag.Parse()
	rand.Seed(time.Now().UnixNano())

	err := opt.generateData()
	if err != nil {
		panic(err)
	}
}

func init() {
	flag.StringVar(&opt.size, "size", "128MB", "Size of the desired database")
	flag.StringVar(&opt.host, "host", "localhost", "MySQL host address")
	flag.IntVar(&opt.port, "port", 3306, "Port number where the MySQL is listening")
	flag.StringVar(&opt.user, "user", "root", "Username to use to connect with the database")
	flag.StringVar(&opt.password, "password", "admin123", "Password to use to connect with the database")
	flag.StringVar(&opt.dbName, "database", "demodata", "Name of the database to create")
	flag.IntVar(&opt.concurrency, "concurrency", 1, "Number of parallel thread to inject data")
}

func (opt *GeneratorOptions) generateData() error {
	// create the database if it does not exist
	err := opt.ensureDatabase()
	if err != nil {
		return err
	}
	remainingSize, err := opt.parseSize()
	if err != nil {
		return err
	}

	const quarterGB = 102400 // 100MB
	for remainingSize > 0 {
		injectable := math.Min(float64(remainingSize), quarterGB)
		fmt.Println("injectable: ", injectable)
		perThreadInjectable := injectable / float64(opt.concurrency)
		fmt.Println("perThreadInjectable: ", perThreadInjectable)
		wg := sync.WaitGroup{}
		for i := 0; i < opt.concurrency; i++ {
			wg.Add(1)
			go opt.createTable(&wg, randSeq(10), int(math.Ceil(perThreadInjectable)))
		}
		wg.Wait()
		// show the database size
		err = opt.showDBSize()
		if err != nil {
			return err
		}
		remainingSize -= quarterGB
	}

	return nil
}

func (opt *GeneratorOptions) ensureDatabase() error {
	db, err := opt.getClient("mysql")
	if err != nil {
		return err
	}
	defer db.Close()

	// ping database to check the connection
	fmt.Println("Pinging the database.....")
	if err := db.Ping(); err != nil {
		return err
	}
	fmt.Println("Ping Succeeded")

	// create the database
	fmt.Printf("Creating database: %q.....\n", opt.dbName)
	if _, err = db.Exec(fmt.Sprintf("CREATE DATABASE %s;", opt.dbName)); err != nil {
		if strings.Contains(err.Error(), "database exists") {
			fmt.Println("Database already exist")
			return nil
		}
		return err
	}
	fmt.Printf("Database %q has been created successfully", opt.dbName)
	return nil
}

func (opt *GeneratorOptions) createTable(wg *sync.WaitGroup, tableName string, rowNumber int) error {
	defer wg.Done()
	db, err := opt.getClient(opt.dbName)
	if err != nil {
		fmt.Println("failed to create db client. Reason: ", err)
		return err
	}
	defer db.Close()

	// create table
	//fmt.Printf("Creating table: %q.....\n", tableName)
	if _, err = db.Exec(fmt.Sprintf("CREATE TABLE %s (id INT NOT NULL, data TEXT, PRIMARY KEY (id))", tableName)); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			fmt.Printf("failed to crate table %q. Reason: %v\n", tableName, err)
			return err
		}
		fmt.Println("Table already exist")
	}

	// insert rows. each row will have (1KB) data
	for i := 0; i < rowNumber; i++ {
		_, err = db.Exec(fmt.Sprintf("INSERT INTO %s (id,data) VALUES (%d,%q)", tableName, i, randSeq(1024)))
		if err != nil {
			fmt.Printf("failed to insert data into table %q. Reason: %v\n", tableName, err)
			return err
		}
	}
	return opt.showTableSize(tableName)
}

func (opt *GeneratorOptions) showDBSize() error {
	statement := fmt.Sprintf("SELECT table_schema, SUM(data_length + index_length) / %d FROM information_schema.TABLES GROUP BY table_schema", 1024*opt.unitCoefficient())
	db, err := opt.getClient("mysql")
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.Query(statement)
	if err != nil {
		return err
	}
	defer rows.Close()

	var dbname, size sql.RawBytes
	fmt.Println("====================== Database Sizes ===================")
	for rows.Next() {
		rows.Scan(&dbname, &size)
		fmt.Printf("%20s: %15s %s\n", string(dbname), string(size), opt.unit)
	}
	return nil
}

func (opt *GeneratorOptions) showTableSize(tableName string) error {
	statement := fmt.Sprintf("SELECT table_schema, round(((data_length + index_length) / %d), 2) FROM information_schema.TABLES WHERE table_schema = %q AND table_name = %q;", 1024, opt.dbName, tableName)
	db, err := opt.getClient(opt.dbName)
	if err != nil {
		fmt.Println("failed to create db client. Reason: ", err)
		return err
	}
	defer db.Close()

	rows, err := db.Query(statement)
	if err != nil {
		fmt.Println("failed to execute query. Reason: ", err)
		return err
	}
	defer rows.Close()

	var dbname, size sql.RawBytes
	for rows.Next() {
		rows.Scan(&dbname, &size)
		fmt.Printf("%20s: %15s KB\n", string(tableName), string(size))
	}
	return nil
}
func (opt *GeneratorOptions) getClient(database string) (*sql.DB, error) {
	dns := fmt.Sprintf("%v:%v@tcp(%v:%v)/%v", opt.user, opt.password, opt.host, opt.port, database)
	db, err := sql.Open("mysql", dns)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func (opt *GeneratorOptions) unitCoefficient() int {
	switch opt.unit {
	case "KB":
		return 1
	case "MB":
		return 1024
	case "GB":
		return 1024 * 1024
	}
	return 1
}

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randSeq(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func (opt *GeneratorOptions) parseSize() (int, error) {
	dataUnit := opt.size[len(opt.size)-2:]
	unitCount, err := strconv.Atoi(opt.size[0 : len(opt.size)-2])
	if err != nil {
		return 0, err
	}
	opt.unit = dataUnit
	return unitCount * opt.unitCoefficient(), nil
}

// X B
// d = min(X,quarterGB)
// pgr = d/c
//
// table = row * m
// 268435456 = 256MB
