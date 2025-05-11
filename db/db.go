package db

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var (
	db   *sql.DB
	mu   sync.Mutex
)

type State struct {
	Ticker    string
	Signal    string
	Position  float64
	LastUpdate time.Time
}

type Transaction struct {
	ID        int
	Ticker    string
	Signal    string
	Amount    float64
	Price     float64
	USDTValue float64
	Timestamp time.Time
}

func InitDB(dataSourceName string) {
	var err error
	db, err = sql.Open("sqlite3", dataSourceName)
	if err != nil {
		log.Fatal(err)
	}

	// Create states table if it doesn't exist
	statesSQL := `
		CREATE TABLE IF NOT EXISTS states (
			ticker TEXT PRIMARY KEY,
			signal TEXT,
			position REAL,
			last_update TIMESTAMP
		)`
	if _, err := db.Exec(statesSQL); err != nil {
		log.Fatal(err)
	}

	// Initialize default pairs if not present
	defaultPairs := []string{"BTCUSDT", "TRXUSDT", "SUIUSDT", "SOLUSDT", "NEARUSDT", "TONUSDT", "ICPUSDT"}
	for _, pair := range defaultPairs {
		if err := initState(pair); err != nil {
			log.Printf("Error initializing state for %s: %v", pair, err)
		}
	}

	// Create transactions table if it doesn't exist
	transactionsSQL := `
		CREATE TABLE IF NOT EXISTS transactions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ticker TEXT,
			signal TEXT,
			amount REAL,
			price REAL,
			usdt_value REAL,
			timestamp TIMESTAMP
		)`
	if _, err := db.Exec(transactionsSQL); err != nil {
		log.Fatal(err)
	}

	// Create account_value table for historical totals
	accountValueSQL := `
		CREATE TABLE IF NOT EXISTS account_value (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			total_usdt REAL,
			timestamp TIMESTAMP
		)`
	if _, err := db.Exec(accountValueSQL); err != nil {
		log.Fatal(err)
	}
}

func initState(ticker string) error {
	mu.Lock()
	defer mu.Unlock()

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM states WHERE ticker = ?", ticker).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		_, err = db.Exec("INSERT INTO states (ticker, signal, position, last_update) VALUES (?, ?, ?, ?)",
			ticker, "sell", 0.0, time.Now())
		if err != nil {
			return err
		}
	}
	return nil
}

func GetState(ticker string) (State, error) {
	mu.Lock()
	defer mu.Unlock()

	var state State
	err := db.QueryRow("SELECT ticker, signal, position, last_update FROM states WHERE ticker = ?", ticker).Scan(
		&state.Ticker, &state.Signal, &state.Position, &state.LastUpdate)
	if err == sql.ErrNoRows {
		return State{}, fmt.Errorf("no state found for %s", ticker)
	}
	if err != nil {
		return State{}, err
	}
	return state, nil
}

func UpdateState(ticker, signal string, position float64) error {
	mu.Lock()
	defer mu.Unlock()

	_, err := db.Exec("UPDATE states SET signal = ?, position = ?, last_update = ? WHERE ticker = ?",
		signal, position, time.Now(), ticker)
	if err != nil {
		return err
	}
	return nil
}

func GetAllStates() ([]State, error) {
	mu.Lock()
	defer mu.Unlock()

	rows, err := db.Query("SELECT ticker, signal, position, last_update FROM states")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var states []State
	for rows.Next() {
		var state State
		if err := rows.Scan(&state.Ticker, &state.Signal, &state.Position, &state.LastUpdate); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return states, nil
}

func RecordTransaction(ticker, signal string, amount, price, usdtValue float64) error {
	mu.Lock()
	defer mu.Unlock()

	_, err := db.Exec("INSERT INTO transactions (ticker, signal, amount, price, usdt_value, timestamp) VALUES (?, ?, ?, ?, ?, ?)",
		ticker, signal, amount, price, usdtValue, time.Now())
	if err != nil {
		return err
	}
	return nil
}

func GetTransactions(ticker string) ([]Transaction, error) {
	mu.Lock()
	defer mu.Unlock()

	rows, err := db.Query("SELECT id, ticker, signal, amount, price, usdt_value, timestamp FROM transactions WHERE ticker = ? ORDER BY timestamp", ticker)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transactions []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.Ticker, &t.Signal, &t.Amount, &t.Price, &t.USDTValue, &t.Timestamp); err != nil {
			return nil, err
		}
		transactions = append(transactions, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return transactions, nil
}

func RecordAccountValue(totalUSDT float64) error {
	mu.Lock()
	defer mu.Unlock()

	_, err := db.Exec("INSERT INTO account_value (total_usdt, timestamp) VALUES (?, ?)", totalUSDT, time.Now())
	if err != nil {
		return err
	}
	return nil
}

func GetAccountValues() ([]struct {
	TotalUSDT float64
	Timestamp time.Time
}, error) {
	mu.Lock()
	defer mu.Unlock()

	rows, err := db.Query("SELECT total_usdt, timestamp FROM account_value ORDER BY timestamp")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []struct {
		TotalUSDT float64
		Timestamp time.Time
	}
	for rows.Next() {
		var v struct {
			TotalUSDT float64
			Timestamp time.Time
		}
		if err := rows.Scan(&v.TotalUSDT, &v.Timestamp); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func Close() {
	if db != nil {
		db.Close()
	}
}