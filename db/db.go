package db

import (
	"database/sql"
	"log"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type TickerState struct {
	Ticker     string
	Signal     string
	Position   float64
	LastUpdate time.Time
}

var (
	db   *sql.DB
	mu   sync.Mutex
)

func InitDB(dbPath string) {
	var err error
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatal(err)
	}

	query := `
	CREATE TABLE IF NOT EXISTS ticker_states (
		ticker TEXT PRIMARY KEY,
		signal TEXT,
		position FLOAT,
		last_update DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = db.Exec(query)
	if err != nil {
		log.Fatal(err)
	}

	// Initialize default 7 USDT pairs if not present
	defaultPairs := []string{"BTCUSDT", "TRXUSDT", "SUIUSDT", "SOLUSDT", "NEARUSDT", "TONUSDT", "ICPUSDT"}
	for _, pair := range defaultPairs {
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM ticker_states WHERE ticker = ?", pair).Scan(&count)
		if err != nil {
			log.Fatal(err)
		}
		if count == 0 {
			_, err = db.Exec("INSERT INTO ticker_states (ticker, signal, position) VALUES (?, ?, ?)", pair, "sell", 0.0)
			if err != nil {
				log.Fatal(err)
			}
		}
	}
}

func GetState(ticker string) (TickerState, error) {
	mu.Lock()
	defer mu.Unlock()

	var state TickerState
	err := db.QueryRow("SELECT ticker, signal, position, last_update FROM ticker_states WHERE ticker = ?", ticker).Scan(&state.Ticker, &state.Signal, &state.Position, &state.LastUpdate)
	if err != nil {
		return TickerState{}, err
	}
	return state, nil
}

func UpdateState(ticker, signal string, position float64) error {
	mu.Lock()
	defer mu.Unlock()

	_, err := db.Exec("INSERT OR REPLACE INTO ticker_states (ticker, signal, position, last_update) VALUES (?, ?, ?, ?)", ticker, signal, position, time.Now())
	return err
}

func GetAllStates() ([]TickerState, error) {
	mu.Lock()
	defer mu.Unlock()

	rows, err := db.Query("SELECT ticker, signal, position, last_update FROM ticker_states")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var states []TickerState
	for rows.Next() {
		var state TickerState
		err = rows.Scan(&state.Ticker, &state.Signal, &state.Position, &state.LastUpdate)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func Close() {
	if db != nil {
		err := db.Close()
		if err != nil {
			log.Printf("Error closing database: %v", err)
		}
	}
}