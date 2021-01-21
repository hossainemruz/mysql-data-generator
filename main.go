package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type GeneratorOptions struct {
	size         string
	host         string
	port         int
	user         string
	password     string
	concurrency  int
	dbName       string
	tableNumber  int
	columnNumber int
	rowNumber    int
	overwrite    bool
}

const (
	OneKB = 1024
	OneMB = 1024 * 1024
	OneGB = 1024 * 1024 * 1024
)

var opt = GeneratorOptions{}

var payload []byte

func main() {
	flag.Parse()
	rand.Seed(time.Now().UnixNano())

	if opt.user == "" {
		opt.user = os.Getenv("USERNAME")
	}
	if opt.password == "" {
		opt.password = os.Getenv("PASSWORD")
	}
	err := opt.generateData()
	if err != nil {
		panic(err)
	}
}

func init() {
	flag.StringVar(&opt.size, "size", "128MB", "Size of the desired database")
	flag.StringVar(&opt.host, "host", "localhost", "MySQL host address")
	flag.IntVar(&opt.port, "port", 3306, "Port number where the MySQL is listening")
	flag.StringVar(&opt.user, "user", "", "Username to use to connect with the database")
	flag.StringVar(&opt.password, "password", "", "Password to use to connect with the database")
	flag.StringVar(&opt.dbName, "database", "demodata", "Name of the database to create")
	flag.IntVar(&opt.concurrency, "concurrency", 1, "Number of parallel thread to inject data")
	flag.IntVar(&opt.tableNumber, "tables", 1, "Number of tables to insert in the database")
	flag.BoolVar(&opt.overwrite, "overwrite", false, "Drop previous database/table (if they exist) before inserting new one.")
	//flag.IntVar(&opt.rowNumber, "rows", 0, "Number of rows to insert in each table")
	//flag.IntVar(&opt.columnNumber, "columns", 2, "Number of columns to insert in the tables")

	payload = make([]byte, (1024*1024)/2)
	rand.Read(payload)
}

func (opt *GeneratorOptions) generateData() error {
	startingTime := time.Now()
	// create the database if it does not exist
	err := opt.ensureDatabase()
	if err != nil {
		return err
	}
	// ensure tables
	for i := 1; i <= opt.tableNumber; i++ {
		err := opt.ensureTable(fmt.Sprintf("table%d", i))
		if err != nil {
			return err
		}
	}
	desiredAmount, err := opt.parseSize()
	if err != nil {
		return err
	}
	fmt.Println("Generating sample data......................")
	totalRows := desiredAmount / (opt.tableNumber * 1024 * 1024) // 1MB per rows
	fmt.Println("Number of rows to insert: ", totalRows)
	// insert tables
	workerLimiter := make(chan struct{}, opt.concurrency)

	// start separate go routine for showing progress
	initialSize, err := opt.getDatabaseSize()
	if err != nil {
		return err
	}
	done := make(chan bool)
	go func() {
		err := opt.showProgress(initialSize, desiredAmount, done)
		if err != nil {
			fmt.Println(err)
		}
	}()
	wg := sync.WaitGroup{}
	mu := sync.Mutex{}
	maxActiveWorkers := 0
	activeWorkers := 0
	for i := 0; i < totalRows; i++ {
		workerLimiter <- struct{}{} // stuck if already maximum number of workers are already running
		wg.Add(1)
		go func() {
			// start a worker
			defer wg.Done()
			mu.Lock()
			activeWorkers++
			if activeWorkers > maxActiveWorkers {
				maxActiveWorkers = activeWorkers
			}
			mu.Unlock()
			tableName := fmt.Sprintf("table%d", (rand.Int()%opt.tableNumber)+1)
			for try := 0; try < 100; try++ {
				err := opt.insertRow(tableName)
				// only retry if too many connection
				if err != nil && strings.Contains(err.Error(), "Too many connections") {
					time.Sleep(5 * time.Second)
					continue
				}
				break
			}
			mu.Lock()
			activeWorkers--
			mu.Unlock()
			<-workerLimiter // worker has done it's work. so release the seat.
			if err != nil {
				fmt.Printf("Failed to insert a row in table: %q. Reason: %v.\n", tableName, err)
			}
		}()
	}
	// wait for all go routines to complete
	wg.Wait()
	// close the progress routine
	done <- true
	// show final percentage
	fmt.Println("Successfully inserted demo data....")
	totalTime := time.Since(startingTime)
	curSize, err := opt.getDatabaseSize()
	if err != nil {
		return err
	}

	fmt.Println("\n=========================== Summery ===========================")
	fmt.Printf("%35s: %s\n", "Total data inserted", formatSize(curSize-initialSize))
	fmt.Printf("%35s: %s\n", "Total time taken", totalTime.String())
	fmt.Printf("%35s: %d\n", "Max simultaneous go-routine", maxActiveWorkers)
	fmt.Printf("%35s: %s/s\n", "Speed", formatSize((curSize-initialSize)/int(totalTime.Seconds())))

	fmt.Println("\n====================== Current Database Sizes =================")
	err = opt.showDBSize()
	if err != nil {
		return err
	}
	return nil
}

func (opt *GeneratorOptions) ensureDatabase() error {
	db, err := opt.getClient("mysql")
	if err != nil {
		return err
	}
	defer db.Close()

	// See "Important settings" section.
	db.SetConnMaxLifetime(2 * time.Second)
	db.SetMaxOpenConns(120)
	db.SetMaxIdleConns(130)

	// ping database to check the connection
	fmt.Println("Pinging the database.....")
	if err := db.Ping(); err != nil {
		return err
	}
	fmt.Println("Ping Succeeded")

	if opt.overwrite {
		fmt.Printf("Dropping database: %s\n", opt.dbName)
		if _, err := db.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s;", opt.dbName)); err != nil {
			return err
		}
	}

	// create the database
	fmt.Printf("Creating database: %q.....\n", opt.dbName)
	if _, err = db.Exec(fmt.Sprintf("CREATE DATABASE %s;", opt.dbName)); err != nil {
		if strings.Contains(err.Error(), "database exists") {
			fmt.Println("Database already exist")
			return nil
		}
		return err
	}
	fmt.Printf("Database %q has been created successfully\n", opt.dbName)
	return nil
}

func (opt *GeneratorOptions) ensureTable(tableName string) error {
	db, err := opt.getClient(opt.dbName)
	if err != nil {
		return err
	}
	defer db.Close()

	// create table
	statement := fmt.Sprintf("CREATE TABLE %s (id VARCHAR(256), data MEDIUMTEXT, PRIMARY KEY (id))", tableName)
	if _, err = db.Exec(statement); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("failed to crate table %q. Reason: %v\n", tableName, err)
		}
		fmt.Println("Table already exist")
	}
	return nil
}

func (opt *GeneratorOptions) insertRow(tableName string) error {
	db, err := opt.getClient(opt.dbName)
	if err != nil {
		return err
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	defer db.Close()

	statement := fmt.Sprintf("INSERT INTO %s (id,data) VALUES (%q,%q)",
		tableName,
		randSeq(10),
		payload,
	)
	_, err = db.Exec(statement)
	if err != nil {
		return err
	}
	return nil
}

func (opt *GeneratorOptions) showDBSize() error {
	statement := fmt.Sprintf("SELECT table_schema, round(SUM(data_length + index_length)) FROM information_schema.TABLES GROUP BY table_schema")
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
	for rows.Next() {
		rows.Scan(&dbname, &size)
		sizeInByte, err := strconv.Atoi(string(size))
		if err != nil {
			return err
		}
		fmt.Printf("%35s: %s\n", string(dbname), formatSize(sizeInByte))
	}
	return nil
}

func (opt *GeneratorOptions) showTableSize(tableName string) error {
	statement := fmt.Sprintf("SELECT table_schema, round(SUM(data_length + index_length)) FROM information_schema.TABLES WHERE table_schema = %q AND table_name = %q;", opt.dbName, tableName)
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
		sizeInByte, err := strconv.Atoi(string(size))
		if err != nil {
			return err
		}
		fmt.Printf("%20s: %15s\n", tableName, formatSize(sizeInByte))
	}
	return nil
}

func (opt *GeneratorOptions) showProgress(initialSize, desiredAmount int, done <-chan bool) error {
	ticker := time.NewTicker(5 * time.Second)
	previousSize := initialSize
	for {
		select {
		case <-done:
			return nil
		case <-ticker.C:
			curSize, err := opt.getDatabaseSize()
			if err != nil {
				return err
			}
			dataInserted := curSize - initialSize
			progress := float64(dataInserted) * 100 / float64(desiredAmount)
			if curSize > previousSize {
				fmt.Printf("Progress: %.2f%% Data Inserted: %s Current %q Size: %s\n", progress, formatSize(dataInserted), opt.dbName, formatSize(curSize))
				previousSize = curSize
			}
		}
	}
}

func (opt *GeneratorOptions) getDatabaseSize() (int, error) {
	db, err := opt.getClient(opt.dbName)
	if err != nil {
		fmt.Println("failed to create db client. Reason: ", err)
		return 0, err
	}
	defer db.Close()
	// make sure the table statistics has been updated
	// refs:
	// - https://dba.stackexchange.com/questions/236863/wrong-innodb-table-status-size-rows-after-updating-from-mysql-5-7-to-8
	// - https://dev.mysql.com/doc/refman/8.0/en/check-table.html
	// - https://dev.mysql.com/doc/refman/8.0/en/analyze-table.html
	tables := make([]string, 0)
	for i := 1; i <= opt.tableNumber; i++ {
		tables = append(tables, fmt.Sprintf("table%d", i))
	}
	_, err = db.Query(fmt.Sprintf("CHECK TABLE %s;", strings.Join(tables, ",")))
	if err != nil {
		return 0, err
	}

	_, err = db.Query(fmt.Sprintf("ANALYZE TABLE %s;", strings.Join(tables, ",")))
	if err != nil {
		return 0, err
	}

	statement := fmt.Sprintf("SELECT table_schema, round(SUM(data_length + index_length)) FROM information_schema.TABLES WHERE table_schema = %q;", opt.dbName)
	rows, err := db.Query(statement)
	if err != nil {
		fmt.Println("failed to execute query. Reason: ", err)
		return 0, err
	}
	defer rows.Close()

	var dbname, size sql.RawBytes
	for rows.Next() {
		rows.Scan(&dbname, &size)
		if size != nil {
			sizeInByte, err := strconv.Atoi(string(size))
			if err != nil {
				fmt.Println("Failed parsing size. reason: ", err)
				return 0, err
			}
			return sizeInByte, nil
		}
	}
	return 0, nil
}

func (opt *GeneratorOptions) getClient(database string) (*sql.DB, error) {
	dns := fmt.Sprintf("%v:%v@tcp(%v:%v)/%v", opt.user, opt.password, opt.host, opt.port, database)
	db, err := sql.Open("mysql", dns)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func formatSize(size int) string {
	switch {
	case size <= OneKB:
		return fmt.Sprintf("%.3f B", float64(size))
	case size <= OneMB:
		return fmt.Sprintf("%.3f KB", float64(size)/OneKB)
	case size <= OneGB:
		return fmt.Sprintf("%.3f MB", float64(size)/OneMB)
	default:
		return fmt.Sprintf("%.3f GB", float64(size)/OneGB)
	}
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
	switch dataUnit {
	case "KB":
		return unitCount * 1024, nil
	case "MB":
		return unitCount * 1024 * 1024, nil
	case "GB":
		return unitCount * 1024 * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("expected data unit to one of (KB, MB, GB). Found: %s", dataUnit)
	}
}
