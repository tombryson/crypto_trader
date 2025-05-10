package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"sync"

	"crypto_trader/db"
	"crypto_trader/okx"
)

type Alert struct {
	Ticker string `json:"ticker"`
	Signal string `json:"signal"`
}

var (
	mu            sync.Mutex
	defaultPairs  = []string{"BTC-USDT", "ETH-USDT", "BNB-USDT", "XRP-USDT", "ADA-USDT", "SOL-USDT", "DOGE-USDT"}
)

func handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var alert Alert
	if err := json.NewDecoder(r.Body).Decode(&alert); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	client := okx.NewClient(
		os.Getenv("OKX_API_KEY"),
		os.Getenv("OKX_SECRET_KEY"),
		os.Getenv("OKX_PASSPHRASE"),
	)

	mu.Lock()
	defer mu.Unlock()

	// Get current state from DB
	currentState, err := db.GetState(alert.Ticker)
	if err != nil {
		log.Printf("Error getting state for %s: %v", alert.Ticker, err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Skip if state hasn't changed
	if currentState.Signal == alert.Signal {
		log.Printf("Ticker %s already in %s state, skipping order", alert.Ticker, alert.Signal)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Alert processed (no action): %s %s", alert.Ticker, alert.Signal)
		return
	}

	// Get current spot balance and positions
	spotBalance, err := client.GetSpotBalance()
	if err != nil {
		log.Printf("Error getting spot balance: %v", err)
		http.Error(w, "Failed to get balance", http.StatusInternalServerError)
		return
	}
	positions, err := client.GetPositions()
	if err != nil {
		log.Printf("Error getting positions: %v", err)
		http.Error(w, "Failed to get positions", http.StatusInternalServerError)
		return
	}

	// Calculate total allocated and available funds
	var totalCryptoValue float64
	for _, pair := range defaultPairs {
		if pos, ok := positions[pair]; ok {
			totalCryptoValue += pos // Approximate value; in reality, multiply by current price
		}
	}
	availableFunds := spotBalance + totalCryptoValue

	// Count buy signals
	buyCount := 0
	for _, pair := range defaultPairs {
		state, _ := db.GetState(pair)
		if state.Signal == "buy" {
			buyCount++
		}
	}

	// Adjust positions based on new signal
	if alert.Signal == "buy" {
		if buyCount == 0 {
			// First buy signal, allocate all spot funds
			size := spotBalance / getCurrentPrice(alert.Ticker) // Simplified; needs real price API
			err = client.PlaceOrder(alert.Ticker, "buy", size)
		} else {
			// Calculate equal allocation
			targetAllocation := availableFunds / float64(buyCount+1)
			currentPos := currentState.Position
			targetPos := targetAllocation / getCurrentPrice(alert.Ticker) // Simplified

			if currentPos > 0 {
				// Sell excess if needed
				if currentPos > targetPos {
					sellSize := currentPos - targetPos
					err = client.PlaceOrder(alert.Ticker, "sell", sellSize)
				}
			} else {
				// Buy new position
				buySize := targetPos
				err = client.PlaceOrder(alert.Ticker, "buy", buySize)
			}
		}
	} else if alert.Signal == "sell" {
		if currentState.Position > 0 {
			err = client.PlaceOrder(alert.Ticker, "sell", currentState.Position)
		}
	}

	if err != nil {
		log.Printf("Error placing order: %v", err)
		http.Error(w, "Failed to place order", http.StatusInternalServerError)
		return
	}

	// Update state in DB (position update would require price data)
	db.UpdateState(alert.Ticker, alert.Signal, 0) // Placeholder; update with real position

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Alert processed: %s %s", alert.Ticker, alert.Signal)
}

func stateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mu.Lock()
	defer mu.Unlock()

	states, err := db.GetAllStates()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		log.Printf("Error getting states: %v", err)
		return
	}

	tmpl := template.Must(template.New("state").Parse(`
		<!DOCTYPE html>
		<html>
		<head>
			<title>Ticker States</title>
			<style>
				table { border-collapse: collapse; width: 50%; }
				th, td { border: 1px solid #ddd; padding: 8px; text-align: left; }
				th { background-color: #f2f2f2; }
				.buy { color: green; }
				.sell { color: red; }
			</style>
		</head>
		<body>
			<h1>Ticker States</h1>
			<table>
				<tr>
					<th>Ticker</th>
					<th>Signal</th>
					<th>Position</th>
					<th>Last Update</th>
				</tr>
				{{range .}}
				<tr>
					<td>{{.Ticker}}</td>
					<td class="{{.Signal}}">{{.Signal}}</td>
					<td>{{printf "%.2f" .Position}}</td>
					<td>{{.LastUpdate.Format "2006-01-02 15:04:05"}}</td>
				</tr>
				{{end}}
			</table>
		</body>
		</html>
	`))

	err = tmpl.Execute(w, states)
	if err != nil {
		http.Error(w, "Failed to render state", http.StatusInternalServerError)
		log.Printf("Template error: %v", err)
	}
}

func getCurrentPrice(ticker string) float64 {
	// Placeholder: Replace with OKX market data API (e.g., /api/v5/market/ticker)
	// For now, return a dummy price
	return 60000.0 // Example price for BTC-USDT
}

func main() {
	db.InitDB("/data/crypto_trader.db")
	defer db.Close()

	http.HandleFunc("/webhook", handler)
	http.HandleFunc("/state", stateHandler)
	port := ":8080"
	log.Printf("Server starting on port %s...", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}