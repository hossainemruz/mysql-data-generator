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
}

const (
	OneKB = 1024
	OneMB = 1024 * 1024
	OneGB = 1024 * 1024 * 1024
)

var opt = GeneratorOptions{}

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
	flag.IntVar(&opt.tableNumber, "tables", 1, "Number of tables to insert in the database")
	flag.IntVar(&opt.rowNumber, "rows", 1, "Number of rows to insert in each table")
	flag.IntVar(&opt.columnNumber, "columns", 2, "Number of columns to insert in the tables")
}

func (opt *GeneratorOptions) generateData() error {
	// create the database if it does not exist
	err := opt.ensureDatabase()
	if err != nil {
		return err
	}
	desiredAmount, err := opt.parseSize()
	if err != nil {
		return err
	}

	fmt.Println("Generating sample data")
	minTableSize := float64(opt.rowNumber * 1024) // enforce each row should be 1KB
	tableSize := math.Max(minTableSize, float64(desiredAmount)/float64(opt.tableNumber))
	fmt.Println("actual: ", float64(desiredAmount)/float64(opt.tableNumber))
	fmt.Println("minimum: ", minTableSize)
	fmt.Println("tableSize: ", tableSize)

	requiredTables := int(math.Round(float64(desiredAmount) / tableSize))
	fmt.Println("required tables: ", requiredTables)
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
	for j := 0; j < requiredTables; j++ {
		workerLimiter <- struct{}{} // stuck if already maximum number of workers are already running
		wg.Add(1)
		go func() {
			// start a worker
			defer wg.Done()
			err := opt.createTable(randSeq(10), tableSize)
			if err != nil {
				fmt.Println("Failed to create a table. Reason: ", err)
			}
			<-workerLimiter // worker has done it's work. so release the seat.
		}()
	}
	// wait for all go routines to complete
	wg.Wait()
	// close the progress routine
	done <- true
	// show final percentage
	fmt.Println("Successfully inserted demo data....")
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
	fmt.Printf("Database %q has been created successfully\n", opt.dbName)
	return nil
}

func (opt *GeneratorOptions) createTable(tableName string, tableSize float64) error {
	db, err := opt.getClient(opt.dbName)
	if err != nil {
		return err
	}
	defer db.Close()

	columnNames := make([]string, 0)
	columnDefinitions := make([]string, 0)
	for i := 1; i < opt.columnNumber; i++ {
		columnNames = append(columnNames, fmt.Sprintf("column%d", i))
		columnDefinitions = append(columnDefinitions, fmt.Sprintf("column%d TEXT", i))
	}

	rowSize := math.Max(1024, tableSize/float64(opt.rowNumber)) // enforce row size to be minimum 1KB
	columnSize := int(math.Max(1, math.Round(rowSize/float64(opt.columnNumber-1))))
	//fmt.Println("rowSize: ", rowSize)
	//fmt.Println("columnSize: ", columnSize)
	// create table
	statement := fmt.Sprintf("CREATE TABLE %s (id INT NOT NULL,%s, PRIMARY KEY (id))", tableName, strings.Join(columnDefinitions, ","))
	if _, err = db.Exec(statement); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			fmt.Printf("failed to crate table %q. Reason: %v\n", tableName, err)
			return err
		}
		fmt.Println("Table already exist")
	}

	// insert rows. each row will have (1KB) data
	for i := 0; i < opt.rowNumber; i++ {
		columnsData := make([]string, 0)
		for j := 0; j < opt.columnNumber-1; j++ {
			columnsData = append(columnsData, fmt.Sprintf("%q", randSeq(columnSize)))
		}
		statement := fmt.Sprintf("INSERT INTO %s (id,%s) VALUES (%d,%s)",
			tableName,
			strings.Join(columnNames, ","),
			i,
			strings.Join(columnsData, ","),
		)
		_, err = db.Exec(statement)
		if err != nil {
			fmt.Println(statement)
			return err
		}
	}
	return opt.showTableSize(tableName)
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
		fmt.Printf("%20s: %15s\n", string(dbname), formatSize(sizeInByte))
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
				fmt.Printf("Current Size (%s): %s Data Inserted: %s Progress: %.3f%%\n", opt.dbName, formatSize(curSize), formatSize(dataInserted), progress)
				previousSize = curSize
			}
		}
	}
}

func (opt *GeneratorOptions) getDatabaseSize() (int, error) {
	statement := fmt.Sprintf("SELECT table_schema, round(SUM(data_length + index_length)) FROM information_schema.TABLES WHERE table_schema = %q;", opt.dbName)
	db, err := opt.getClient(opt.dbName)
	if err != nil {
		fmt.Println("failed to create db client. Reason: ", err)
		return 0, err
	}
	defer db.Close()

	rows, err := db.Query(statement)
	if err != nil {
		fmt.Println("failed to execute query. Reason: ", err)
		return 0, err
	}
	defer rows.Close()

	var dbname, size sql.RawBytes
	for rows.Next() {
		rows.Scan(&dbname, &size)
		if size!=nil{
			fmt.Println(string(size))
			sizeInByte, err := strconv.Atoi(string(size))
			if err != nil {
				fmt.Println("Failed parsing size. reason: ",err)
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

// X B
// d = min(X,quarterGB)
// pgr = d/c
//
// table = row * m
// 268435456 = 256MB
