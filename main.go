package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
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
	tableNumber int
	dbName      string
	overwrite   bool
}

const (
	OneKB = 1024
	OneMB = 1024 * 1024
	OneGB = 1024 * 1024 * 1024
)

var opt = GeneratorOptions{}

var db *sql.DB

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
	flag.StringVar(&opt.dbName, "database", "sampleData", "Name of the database to create")
	flag.IntVar(&opt.concurrency, "concurrency", 1, "Number of parallel thread to inject data")
	flag.IntVar(&opt.tableNumber, "tables", 1, "Number of tables to insert in the database")
	flag.BoolVar(&opt.overwrite, "overwrite", false, "Drop previous database/table (if they exist) before inserting new one.")
}

func (opt *GeneratorOptions) generateData() error {
	startingTime := time.Now()
	// create the database if it does not exist
	err := opt.ensureDatabase()
	if err != nil {
		return err
	}
	db, err = opt.getClient(opt.dbName)
	if err != nil {
		return err
	}
	maxConnection:=int(math.Max(140, float64(opt.concurrency+10)))
	db.SetConnMaxLifetime(24 * time.Hour)
	db.SetMaxOpenConns(maxConnection)
	db.SetMaxIdleConns(maxConnection)
	//defer db.Close()

	// create tables
	for i := 0; i < opt.tableNumber; i++ {
		tableName := fmt.Sprintf("table%d", i)
		statement := fmt.Sprintf("CREATE TABLE %s (id int NOT NULL AUTO_INCREMENT PRIMARY KEY,name text, height int, weight int, age int,description Text)", tableName)
		if _, err = db.Exec(statement); err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				return fmt.Errorf("failed to crate table %q. Reason: %v\n", tableName, err)
			}
			fmt.Println("Table already exist")
		}
	}

	// parse desired data size
	desiredAmount, err := opt.parseSize()
	if err != nil {
		return err
	}

	fmt.Println("Generating sample data......................")
	initialSize, err := opt.getDatabaseSize()
	if err != nil {
		return err
	}

	// start go routines to insert data in parallel
	ctx, cancel := context.WithCancel(context.Background())
	wg := sync.WaitGroup{}
	for i := 0; i < opt.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := opt.insertRows(ctx)
			if err != nil {
				fmt.Println("Err: ", err)
			}
		}()
	}

	// monitor progress and stop data insertion when 100% completed
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			fmt.Println("Stopping data insertion...")
			cancel()
		}()
		opt.monitorProgress(initialSize, desiredAmount)
	}()
	wg.Wait()

	// show final statistics
	fmt.Println("Successfully inserted demo data....")
	totalTime := time.Since(startingTime)
	curSize, err := opt.getDatabaseSize()
	if err != nil {
		return err
	}

	fmt.Println("\n=========================== Summery ===========================")
	fmt.Printf("%35s: %s\n", "Total data inserted", formatSize(curSize-initialSize))
	fmt.Printf("%35s: %s\n", "Total time taken", totalTime.String())
	fmt.Printf("%35s: %s/s\n", "Speed", formatSize((curSize-initialSize)/int(totalTime.Seconds())))

	fmt.Println("\n====================== Current Database Sizes =================")
	return opt.showDBSizes()
}

func (opt *GeneratorOptions) ensureDatabase() error {
	mydb, err := opt.getClient("mysql")
	if err != nil {
		return err
	}
	defer mydb.Close()

	//set settings
	//mydb.SetConnMaxLifetime(2 * time.Hour)
	//mydb.SetMaxOpenConns(opt.concurrency + 10)
	//mydb.SetMaxIdleConns(120)

	// ping database to check the connection
	fmt.Println("Pinging the database.....")
	if err := mydb.Ping(); err != nil {
		return err
	}
	fmt.Println("Ping Succeeded")

	if opt.overwrite {
		fmt.Printf("Dropping database: %s\n", opt.dbName)
		if _, err := mydb.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s;", opt.dbName)); err != nil {
			return err
		}
	}

	// create the database
	fmt.Printf("Creating database: %q.....\n", opt.dbName)
	if _, err = mydb.Exec(fmt.Sprintf("CREATE DATABASE %s;", opt.dbName)); err != nil {
		if strings.Contains(err.Error(), "database exists") {
			fmt.Println("Database already exist")
			return nil
		}
		return err
	}
	fmt.Printf("Database %q has been created successfully\n", opt.dbName)
	return nil
}

func (opt *GeneratorOptions) insertRows(ctx context.Context) error {
	//db.SetConnMaxLifetime(2 * time.Hour)
	//db.SetMaxOpenConns(opt.concurrency + 10)
	//db.SetMaxIdleConns(120)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			tableName := fmt.Sprintf("table%d", rand.Int()%opt.tableNumber)
			statement := fmt.Sprintf("INSERT INTO %s (name,height,weight,age,description) VALUES (%q,%d,%d,%d,%q)",
				tableName,
				generateName(),
				120+rand.Int()%81,
				30+rand.Int()%201,
				10+rand.Int()%101,
				loremIpsum,
			)
			_, err := db.Exec(statement)
			if err != nil {
				fmt.Printf("Failed to insert row into table: %s. Reason: %v.\n", tableName, err)
			}
		}
	}
}

func (opt *GeneratorOptions) showDBSizes() error {
	statement := fmt.Sprintf("SELECT table_schema, round(SUM(data_length + index_length)) FROM information_schema.TABLES GROUP BY table_schema")
	rows, err := db.Query(statement)
	if err != nil {
		return err
	}
	defer rows.Close()

	var dbname, size sql.RawBytes
	for rows.Next() {
		err := rows.Scan(&dbname, &size)
		if err != nil {
			return err
		}
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
	//db.SetConnMaxLifetime(2 * time.Minute)
	//db.SetMaxOpenConns(10)
	//db.SetMaxIdleConns(020)

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

func (opt *GeneratorOptions) monitorProgress(initialSize, desiredAmount int) {
	fmt.Println("Current Database Size: ", formatSize(initialSize), " Desired Amount to Inject: ", formatSize(desiredAmount))
	ticker := time.NewTicker(1 * time.Second)
	previousSize := initialSize
	for {
		select {
		case <-ticker.C:
			curSize, err := opt.getDatabaseSize()
			if err != nil {
				fmt.Println("Failed to get database size. Reason: ", err)
				continue
			}
			dataInserted := curSize - initialSize
			progress := float64(dataInserted) * 100 / float64(desiredAmount)
			if curSize > previousSize {
				fmt.Printf("Progress: %.2f%% Data Inserted: %s Current %q Size: %s\n", progress, formatSize(dataInserted), opt.dbName, formatSize(curSize))
				previousSize = curSize
			}
			if progress >= 100 {
				fmt.Println("Successfully inserted sample data......")
				return
			}
		}
	}
}

func (opt *GeneratorOptions) getDatabaseSize() (int, error) {
	// make sure the table statistics has been updated
	// refs:
	// - https://dba.stackexchange.com/questions/236863/wrong-innodb-table-status-size-rows-after-updating-from-mysql-5-7-to-8
	// - https://dev.mysql.com/doc/refman/8.0/en/check-table.html
	// - https://dev.mysql.com/doc/refman/8.0/en/analyze-table.html
	tables := make([]string, 0)
	for i := 0; i < opt.tableNumber; i++ {
		tables = append(tables, fmt.Sprintf("table%d", i))
	}
	_, err := db.Query(fmt.Sprintf("CHECK TABLE %s;", strings.Join(tables, ",")))
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
func generateName() string {
	return fmt.Sprintf("%s %s", strings.Title(adjectives[rand.Int()%totalAdjectives]), strings.Title(nouns[rand.Int()%totalNouns]))
}

func (opt *GeneratorOptions) parseSize() (int, error) {
	var amount float64
	var unit string
	_, err := fmt.Sscanf(opt.size, "%f%s", &amount, &unit)
	if err != nil {
		return 0, err
	}

	switch unit {
	case "KB", "K":
		return int(amount * 1024), nil
	case "MB", "Mi":
		return int(amount * 1024 * 1024), nil
	case "GB", "Gi":
		return int(amount * 1024 * 1024 * 1024), nil
	default:
		return 0, fmt.Errorf("expected data unit to one of (KB,K, MB,Mi, GB,Gi). Found: %s", unit)
	}
}

var (
	adjectives      = []string{"affable", "affectionate", "agreeable", "ambitious", "amiable", "amicable", "amusing", "brave", "bright", "broad-minded", "calm", "careful", "charming", "communicative", "compassionate", "conscientious", "considerate", "convivial", "courageous", "courteous", "creative", "decisive", "determined", "diligent", "diplomatic", "discreet", "dynamic", "easygoing", "emotional", "energetic", "enthusiastic", "exuberant", "fair-minded", "faithful", "fearless", "forceful", "frank", "friendly", "funny", "generous", "gentle", "good", "gregarious", "hard-working", "helpful", "honest", "humorous", "imaginative", "impartial", "independent", "intellectual", "intelligent", "intuitive", "inventive", "kind", "loving", "loyal", "modest", "neat", "nice", "optimistic", "passionate", "patient", "persistent", "pioneering", "philosophical", "placid", "plucky", "polite", "powerful", "practical", "pro-active", "quick-witted", "quiet", "rational", "reliable", "reserved", "resourceful", "romantic", "self-confident", "self-disciplined", "sensible", "sensitive", "shy", "sincere", "sociable", "straightforward", "sympathetic", "thoughtful", "tidy", "tough", "unassuming", "understanding", "versatile", "warmhearted", "willing", "witty"}
	totalAdjectives = 97

	nouns      = []string{"John", "William", "James", "Charles", "George", "Frank", "Joseph", "Thomas", "Henry", "Robert", "Edward", "Harry", "Walter", "Arthur", "Fred", "Albert", "Samuel", "David", "Louis", "Joe", "Charlie", "Clarence", "Richard", "Andrew", "Daniel", "Ernest", "Will", "Jesse", "Oscar", "Lewis", "Peter", "Benjamin", "Frederick", "Willie", "Alfred", "Sam", "Roy", "Herbert", "Jacob", "Tom", "Elmer", "Carl", "Lee", "Howard", "Martin", "Michael", "Bert", "Herman", "Jim", "Francis", "Harvey", "Earl", "Eugene", "Ralph", "Ed", "Claude", "Edwin", "Ben", "Charley", "Paul", "Edgar", "Isaac", "Otto", "Luther", "Lawrence", "Ira", "Patrick", "Guy", "Oliver", "Theodore", "Hugh", "Clyde", "Alexander", "August", "Floyd", "Homer", "Jack", "Leonard", "Horace", "Marion", "Philip", "Allen", "Archie", "Stephen", "Chester", "Willis", "Raymond", "Rufus", "Warren", "Jessie", "Milton", "Alex", "Leo", "Julius", "Ray", "Sidney", "Bernard", "Dan", "Jerry", "Calvin", "Perry", "Dave", "Anthony", "Eddie", "Amos", "Dennis", "Clifford", "Leroy", "Wesley", "Alonzo", "Garfield", "Franklin", "Emil", "Leon", "Nathan", "Harold", "Matthew", "Levi", "Moses", "Everett", "Lester", "Winfield", "Adam", "Lloyd", "Mack", "Fredrick", "Jay", "Jess", "Melvin", "Noah", "Aaron", "Alvin", "Norman", "Gilbert", "Elijah", "Victor", "Gus", "Nelson", "Jasper", "Silas", "Christopher", "Jake", "Mike", "Percy", "Adolph", "Maurice", "Cornelius", "Felix", "Reuben", "Wallace", "Claud", "Roscoe", "Sylvester", "Earnest", "Hiram", "Otis", "Simon", "Willard", "Irvin", "Mark", "Jose", "Wilbur", "Abraham", "Virgil", "Clinton", "Elbert", "Leslie", "Marshall", "Owen", "Wiley", "Anton", "Morris", "Manuel", "Phillip", "Augustus", "Emmett", "Eli", "Nicholas", "Wilson", "Alva", "Harley", "Newton", "Timothy", "Marvin", "Ross", "Curtis", "Edmund", "Jeff", "Elias", "Harrison", "Stanley", "Columbus", "Lon", "Ora", "Ollie", "Russell", "Pearl", "Solomon", "Arch", "Asa", "Clayton", "Enoch", "Irving", "Mathew", "Nathaniel", "Scott", "Hubert", "Lemuel", "Andy", "Ellis", "Emanuel", "Joshua", "Millard", "Vernon", "Wade", "Cyrus", "Miles", "Rudolph", "Sherman", "Austin", "Bill", "Chas", "Lonnie", "Monroe", "Byron", "Edd", "Emery", "Grant", "Jerome", "Max", "Mose", "Steve", "Gordon", "Abe", "Pete", "Chris", "Clark", "Gustave", "Orville", "Lorenzo", "Bruce", "Marcus", "Preston", "Bob", "Dock", "Donald", "Jackson", "Cecil", "Barney", "Delbert", "Edmond", "Anderson", "Christian", "Glenn", "Jefferson", "Luke", "Neal", "Burt", "Ike", "Myron", "Tony", "Conrad", "Joel", "Matt", "Riley", "Vincent", "Emory", "Isaiah", "Nick", "Ezra", "Green", "Juan", "Clifton", "Lucius", "Porter", "Arnold", "Bud", "Jeremiah", "Taylor", "Forrest", "Roland", "Spencer", "Burton", "Don", "Emmet", "Gustav", "Louie", "Morgan", "Ned", "Van", "Ambrose", "Chauncey", "Elisha", "Ferdinand", "General", "Julian", "Kenneth", "Mitchell", "Allie", "Josh", "Judson", "Lyman", "Napoleon", "Pedro", "Berry", "Dewitt", "Ervin", "Forest", "Lynn", "Pink", "Ruben", "Sanford", "Ward", "Douglas", "Ole", "Omer", "Ulysses", "Walker", "Wilbert", "Adelbert", "Benjiman", "Ivan", "Jonas", "Major", "Abner", "Archibald", "Caleb", "Clint", "Dudley", "Granville", "King", "Mary", "Merton", "Antonio", "Bennie", "Carroll", "Freeman", "Josiah", "Milo", "Royal", "Dick", "Earle", "Elza", "Emerson", "Fletcher", "Judge", "Laurence", "Neil", "Roger", "Seth", "Glen", "Hugo", "Jimmie", "Johnnie", "Washington", "Elwood", "Gust", "Harmon", "Jordan", "Simeon", "Wayne", "Wilber", "Clem", "Evan", "Frederic", "Irwin", "Junius", "Lafayette", "Loren", "Madison", "Mason", "Orval", "Abram", "Aubrey", "Elliott", "Hans", "Karl", "Minor", "Wash", "Wilfred", "Allan", "Alphonse", "Dallas", "Dee", "Isiah", "Jason", "Johnny", "Lawson", "Lew", "Micheal", "Orin", "Addison", "Cal", "Erastus", "Francisco", "Hardy", "Lucien", "Randolph", "Stewart", "Vern", "Wilmer", "Zack", "Adrian", "Alvah", "Bertram", "Clay", "Ephraim", "Fritz", "Giles", "Grover", "Harris", "Isom", "Jesus", "Johnie", "Jonathan", "Lucian", "Malcolm", "Merritt", "Otho", "Perley", "Rolla", "Sandy", "Tomas", "Wilford", "Adolphus", "Angus", "Arther", "Carlos", "Cary", "Cassius", "Davis", "Hamilton", "Harve", "Israel", "Leander", "Melville", "Merle", "Murray", "Pleasant", "Sterling", "Steven", "Axel", "Boyd", "Bryant", "Clement", "Erwin", "Ezekiel", "Foster", "Frances", "Geo", "Houston", "Issac", "Jules", "Larkin", "Mat", "Morton", "Orlando", "Pierce", "Prince", "Rollie", "Rollin", "Sim", "Stuart", "Wilburn", "Bennett", "Casper", "Christ", "Dell", "Egbert", "Elmo", "Fay", "Gabriel", "Hector", "Horatio", "Lige", "Saul", "Smith", "Squire", "Tobe", "Tommie", "Wyatt", "Alford", "Alma", "Alton", "Andres", "Burl", "Cicero", "Dean", "Dorsey", "Enos", "Howell", "Lou", "Loyd", "Mahlon", "Nat", "Omar", "Oran", "Parker", "Raleigh", "Reginald", "Rubin", "Seymour", "Wm", "Young", "Benjamine", "Carey", "Carlton", "Eldridge", "Elzie", "Garrett", "Isham", "Johnson", "Larry", "Logan", "Merrill", "Mont", "Oren", "Pierre", "Rex", "Rodney", "Ted", "Webster", "West", "Wheeler", "Willam", "Al", "Aloysius", "Alvie", "Anna", "Art", "Augustine", "Bailey", "Benjaman", "Beverly", "Bishop", "Clair", "Cloyd", "Coleman", "Dana", "Duncan", "Dwight", "Emile", "Evert", "Henderson", "Hunter", "Jean", "Lem", "Luis", "Mathias", "Maynard", "Miguel", "Mortimer", "Nels", "Norris", "Pat", "Phil", "Rush", "Santiago", "Sol", "Sydney", "Thaddeus", "Thornton", "Tim", "Travis", "Truman", "Watson", "Webb", "Wellington", "Winfred", "Wylie", "Alec", "Basil", "Baxter", "Bertrand", "Buford", "Burr", "Cleveland", "Colonel", "Dempsey", "Early", "Ellsworth", "Fate", "Finley", "Gabe", "Garland", "Gerald", "Herschel", "Hezekiah", "Justus", "Lindsey", "Marcellus", "Olaf", "Olin", "Pablo", "Rolland", "Turner", "Verne", "Volney", "Williams", "Almon", "Alois", "Alonza", "Anson", "Authur", "Benton", "Billie", "Cornelious", "Darius", "Denis", "Dillard", "Doctor", "Elvin", "Emma", "Eric", "Evans", "Gideon", "Haywood", "Hilliard", "Hosea", "Lincoln", "Lonzo", "Lucious", "Lum", "Malachi", "Newt", "Noel", "Orie", "Palmer", "Pinkney", "Shirley", "Sumner", "Terry", "Urban", "Uriah", "Valentine", "Waldo", "Warner", "Wong", "Zeb", "Abel", "Alden", "Archer", "Avery", "Carson", "Cullen", "Doc", "Eben", "Elige", "Elizabeth", "Elmore", "Ernst", "Finis", "Freddie", "Godfrey", "Guss", "Hamp", "Hermann", "Isadore", "Isreal", "Jones", "June", "Lacy", "Lafe", "Leland", "Llewellyn", "Ludwig", "Manford", "Maxwell", "Minnie", "Obie", "Octave", "Orrin", "Ossie", "Oswald", "Park", "Parley", "Ramon", "Rice", "Stonewall", "Theo", "Tillman", "Addie", "Aron", "Ashley", "Bernhard", "Bertie", "Berton", "Buster", "Butler", "Carleton", "Carrie", "Clara", "Clarance", "Clare", "Crawford", "Danial", "Dayton", "Dolphus", "Elder", "Ephriam", "Fayette", "Felipe", "Fernando", "Flem", "Florence", "Ford", "Harlan", "Hayes", "Henery", "Hoy", "Huston", "Ida", "Ivory", "Jonah", "Justin", "Lenard", "Leopold", "Lionel", "Manley", "Marquis", "Marshal", "Mart", "Odie", "Olen", "Oral", "Orley", "Otha", "Press", "Price", "Quincy", "Randall", "Rich", "Richmond", "Romeo", "Russel", "Rutherford", "Shade", "Shelby", "Solon", "Thurman", "Tilden", "Troy", "Woodson", "Worth", "Aden", "Alcide", "Alf", "Algie", "Arlie", "Bart", "Bedford", "Benito", "Billy", "Bird", "Birt", "Bruno", "Burley", "Chancy", "Claus", "Cliff", "Clovis", "Connie", "Creed", "Delos", "Duke", "Eber", "Eligah", "Elliot", "Elton", "Emmitt", "Gene", "Golden", "Hal", "Hardin", "Harman", "Hervey", "Hollis", "Ivey", "Jennie", "Len", "Lindsay", "Lonie", "Lyle", "Mac", "Mal", "Math", "Miller", "Orson", "Osborne", "Percival", "Pleas", "Ples", "Rafael", "Raoul", "Roderick", "Rose", "Shelton", "Sid", "Theron", "Tobias", "Toney", "Tyler", "Vance", "Vivian", "Walton", "Watt", "Weaver", "Wilton", "Adolf", "Albin", "Albion", "Allison", "Alpha", "Alpheus", "Anastacio", "Andre", "Annie", "Arlington", "Armand", "Asberry", "Asbury", "Asher", "Augustin", "Auther", "Author", "Ballard", "Blas", "Caesar", "Candido", "Cato", "Clarke", "Clemente", "Colin", "Commodore", "Cora", "Coy", "Cruz", "Curt", "Damon", "Davie", "Delmar", "Dexter", "Dora", "Doss", "Drew", "Edson", "Elam", "Elihu", "Eliza", "Elsie", "Erie", "Ernie", "Ethel", "Ferd", "Friend", "Garry", "Gary", "Grace", "Gustaf", "Hallie", "Hampton", "Harrie", "Hattie", "Hence", "Hillard", "Hollie", "Holmes", "Hope", "Hyman", "Ishmael", "Jarrett", "Jessee", "Joeseph", "Junious", "Kirk", "Levy", "Mervin", "Michel", "Milford", "Mitchel", "Nellie", "Noble", "Obed", "Oda", "Orren", "Ottis", "Rafe", "Redden", "Reese", "Rube", "Ruby", "Rupert", "Salomon", "Sammie", "Sanders", "Soloman", "Stacy", "Stanford", "Stanton", "Thad", "Titus", "Tracy", "Vernie", "Wendell", "Wilhelm", "Willian", "Yee", "Zeke", "Ab", "Abbott", "Agustus", "Albertus", "Almer", "Alphonso", "Alvia", "Artie", "Arvid", "Ashby", "Augusta", "Aurthur", "Babe", "Baldwin", "Barnett", "Bartholomew", "Barton", "Bernie", "Blaine", "Boston", "Brad", "Bradford", "Bradley", "Brooks", "Buck", "Budd", "Ceylon", "Chalmers", "Chesley", "Chin", "Cleo", "Crockett", "Cyril", "Daisy", "Denver", "Dow", "Duff", "Edie", "Edith", "Elick", "Elie", "Eliga", "Eliseo", "Elroy", "Ely", "Ennis", "Enrique", "Erasmus", "Esau", "Everette", "Firman", "Fleming", "Flora", "Gardner", "Gee", "Gorge", "Gottlieb", "Gregorio", "Gregory", "Gustavus", "Halsey", "Handy", "Hardie", "Harl", "Hayden", "Hays", "Hermon", "Hershel", "Holly", "Hosteen", "Hoyt", "Hudson", "Huey", "Humphrey", "Hunt", "Hyrum", "Irven", "Isam", "Ivy", "Jabez", "Jewel", "Jodie", "Judd", "Julious", "Justice", "Katherine", "Kelly", "Kit", "Knute", "Lavern", "Lawyer", "Layton"}
	totalNouns = 1000
	loremIpsum = "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed quam felis, interdum in porttitor lacinia, ornare id neque. Ut facilisis rutrum dui, in consectetur nisl. Nulla in augue ut velit bibendum tempor nec sed odio. Phasellus quam mi, rhoncus ut vehicula a, sollicitudin imperdiet massa. Mauris eget lacus in tellus semper suscipit nec eget sem.Lorem ipsum dolor sit amet, consectetur adipiscing elit. Ut imperdiet augue augue, quis tincidunt massa ullamcorper sed. Integer sit amet dapibus quam, ut laoreet ipsum. Pellentesque id bibendum ipsum. Maecenas egestas, purus nec dignissim euismod, neque ipsum dapibus purus, eu maximus elit purus quis mauris. Orci varius natoque penatibus et magnis dis parturient montes, nascetur ridiculus mus. Morbi vel tellus iaculis, sodales lectus id, pulvinar nunc.\n\nCras in euismod orci. Vestibulum a ex tincidunt, tincidunt nisi a, pretium eros. Maecenas efficitur porta justo sed gravida. Aliquam mi mi, vehicula quis orci ac, efficitur blandit urna. Class aptent taciti sociosqu ad litora torquent per conubia nostra, per inceptos himenaeos. Phasellus arcu eros, dignissim at elit eget, commodo suscipit quam. Aliquam dictum ipsum in nibh mollis, sit amet semper nibh imperdiet. Mauris hendrerit, lacus id tristique aliquam, ex ipsum consectetur est, ut placerat arcu sapien eget nulla. Maecenas dictum magna quis dapibus rhoncus. Quisque convallis arcu mi, non commodo nulla scelerisque a. Nunc ut felis erat. Morbi vel ante consequat, tincidunt erat in, condimentum orci.\n\nPhasellus porttitor, nunc quis pretium scelerisque, lacus tellus finibus orci, tempor laoreet justo mauris id purus. Donec ex ante, feugiat a dui aliquam, pharetra malesuada lectus. Nullam augue risus, porttitor sit amet volutpat ullamcorper, ornare sed elit. Morbi diam sem, dapibus id ullamcorper scelerisque, porta ut urna. Aenean et mi consectetur, tempor lorem a, vestibulum massa. Pellentesque eu est commodo, sodales erat sed, sagittis leo. Ut nec viverra diam. Nullam urna sem, tincidunt in blandit sit amet, efficitur id erat. Etiam sollicitudin accumsan ante, ac dictum lacus consequat a. Phasellus molestie nunc enim, at pharetra quam efficitur vitae.\n\nDonec ullamcorper, mauris placerat iaculis ullamcorper, libero sapien tincidunt leo, venenatis dignissim neque sapien a turpis. Donec sodales tincidunt turpis a faucibus. Quisque id mi a metus ultricies consectetur. Sed tempus et enim et dapibus. Cras ac pulvinar leo. Nam at tincidunt nulla, eu ornare diam. Proin id viverra augue. Aliquam eget mattis ante, sit amet ornare urna. Praesent iaculis laoreet augue quis pharetra. Duis venenatis elementum neque et suscipit. Etiam commodo tellus eu gravida commodo. Nunc in mattis ligula. Sed eleifend, leo at porta vehicula, ipsum felis sollicitudin magna, non eleifend dui nisl et turpis. Proin ut tortor eu leo interdum laoreet.Lorem ipsum dolor sit amet, consectetur adipiscing elit. In mi nisi, scelerisque semper ligula sit amet, vulputate pulvinar ex. Ut sed nisi blandit velit pretium dapibus a in enim. Cras congue sagittis massa, ac semper tellus imperdiet nec. Donec eu viverra diam, sed faucibus neque. In tincidunt enim sem, et consequat massa laoreet non. Nunc fringilla dolor vel dui mollis scelerisque. Suspendisse fermentum mauris quis ex ultricies, rhoncus scelerisque erat maximus. Phasellus venenatis at dolor quis consectetur. Maecenas facilisis bibendum pellentesque. Nulla tincidunt tincidunt tellus a mollis. Donec bibendum purus sed ipsum pulvinar, et viverra enim fringilla. Aliquam sed laoreet metus, non placerat enim. Nullam eget condimentum metus, id convallis metus.\n\nVestibulum molestie posuere molestie. Aliquam accumsan euismod nulla. Fusce a volutpat urna. Proin efficitur orci at dui aliquet, a ornare justo pulvinar. Morbi nisi nisi, molestie lacinia nisi at, dapibus faucibus nisl. Vestibulum vel scelerisque lorem. Donec viverra orci in auctor fermentum. Phasellus urna libero, suscipit sed rutrum in, vestibulum id elit. Cras auctor auctor magna non mattis. Etiam vel venenatis erat. Quisque nec nisi eu ante porttitor scelerisque quis eget velit. Cras molestie ligula in nulla feugiat, sit amet egestas sapien vulputate. Praesent faucibus auctor congue.\n\nVivamus accumsan hendrerit consequat. Nam auctor turpis arcu, vel maximus justo ultricies ac. Morbi eget finibus dui. Vivamus feugiat fringilla nisl semper rhoncus. Sed ac condimentum risus. Donec et fringilla orci. Vestibulum ante ipsum primis in faucibus orci luctus et ultrices posuere cubilia curae; Donec at mattis sem.\n\nCras placerat orci ut aliquet maximus. Curabitur non dapibus mauris. Ut hendrerit, arcu non laoreet eleifend, mi nunc consequat velit, eu egestas ex metus eget nisi. Maecenas id sodales mauris. Pellentesque vitae sem magna. Maecenas accumsan malesuada nunc, sit amet mattis massa semper sit amet. Vivamus malesuada dapibus quam, ac malesuada justo. Maecenas tristique urna sit amet ante iaculis, ac placerat ipsum blandit.\n\nCras odio odio, molestie sed consequat et, molestie sed elit. Fusce at fringilla libero. Class aptent taciti sociosqu ad litora torquent per conubia nostra, per inceptos himenaeos. Phasellus id pretium libero, interdum auctor sem. Curabitur eleifend varius pellentesque. Phasellus porta tincidunt porta. Nulla facilisis nec velit vel malesuada. Cras blandit neque sed laoreet gravida.\n\nDonec eget diam suscipit, tempus elit et, vehicula sem. Donec lacinia risus eget ligula tincidunt fringilla. Proin ornare convallis tempor. Nullam eget ipsum lacus. Etiam lacinia, leo vitae feugiat euismod, lorem erat accumsan justo, vel porttitor nunc dolor non est. Nunc sagittis auctor lectus, nec volutpat eros varius non. Vivamus eget ultrices mauris. Vestibulum porta nibh in egestas ornare. Sed eu enim congue, dapibus nulla non, rhoncus nisl. Nullam vitae nibh id justo lacinia iaculis. Pellentesque purus justo, gravida sed tellus quis, vehicula porttitor augue. In hac habitasse platea dictumst. Aliquam blandit augue ac posuere commodo. Nullam sollicitudin nisl nec metus egestas tincidunt.\n\nDonec cursus, arcu ut ultricies facilisis, nulla augue semper nulla, sit amet aliquet diam enim ac odio. Mauris lobortis porta nulla. Aliquam enim mauris, blandit in hendrerit id, lacinia eu erat. Aliquam sodales porta mollis. Duis feugiat sodales egestas. Nunc mi elit, varius sed velit at, tempus varius ligula. Morbi augue urna, rhoncus eget tempus eu, lacinia sit amet odio. Aenean posuere leo velit, vestibulum scelerisque erat ullamcorper id. Etiam vestibulum molestie est. Orci varius natoque penatibus et magnis dis parturient montes, nascetur ridiculus mus. Proin at massa faucibus, consequat mauris ut, interdum turpis. Nunc laoreet nibh vel nulla euismod egestas. Integer magna nisi, dignissim non nisl eget, cursus tincidunt ligula. Sed in pretium tortor, sed imperdiet quam. Vestibulum aliquam arcu dui, quis varius erat vehicula in. Donec rhoncus sit amet ipsum nec pharetra.\n\nPhasellus vulputate faucibus laoreet. Vestibulum imperdiet risus eu est facilisis, sed facilisis velit dapibus. Aenean maximus tristique tortor, non consequat risus rutrum in. Praesent sollicitudin, nunc vitae euismod ullamcorper, augue libero commodo nisi, sed imperdiet arcu tortor non ligula. Aliquam vitae viverra est. Aenean egestas, nibh vel posuere sagittis, elit est bibendum tortor, a ornare nisi est vitae eros. Phasellus sagittis pulvinar sapien, vitae faucibus mi. Aenean fringilla ipsum nunc. Pellentesque condimentum, est non dignissim convallis, ipsum orci vulputate nunc, vitae semper tellus dui quis nibh. Quisque rhoncus magna volutpat vestibulum luctus.\n\nNam sed augue augue. Praesent hendrerit mauris non libero posuere, vitae efficitur purus varius. Etiam quis varius metus. Nam pellentesque sapien id elit suscipit iaculis. Nulla interdum elit gravida, lobortis ligula et, maximus orci. Nullam non turpis sed libero tempus laoreet. Vestibulum odio augue, tincidunt eget nulla vel, scelerisque malesuada metus. Aenean id nulla sed nunc bibendum molestie. Aliquam eget dolor tempor neque pharetra elementum. Etiam fermentum mattis augue non imperdiet. Nunc malesuada ante metus, ac pulvinar odio vehicula nec. Nunc placerat mi non nisi pharetra eleifend. Mauris maximus urna quis ante varius, ut ultrices massa aliquet.\n\nNullam turpis nisi, eleifend vehicula enim vel, tempus sodales neque. Duis non fringilla arcu, id ultrices nunc. Morbi porta odio et urna lacinia tincidunt. Nullam pellentesque lorem vitae purus laoreet consectetur. Maecenas eget sapien finibus, condimentum erat at, pulvinar mi. Mauris mattis ex tincidunt sapien semper, et iaculis libero rhoncus. Vivamus egestas, nulla et pharetra suscipit, mi purus vehicula nulla, nec pretium mauris purus molestie magna. Aliquam erat volutpat. Nulla et libero in libero porttitor convallis. In congue hendrerit arcu, sed blandit lacus pretium eleifend."
)
