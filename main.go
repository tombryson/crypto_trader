package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"crypto_trader/db"
	"crypto_trader/okx"
)

type Alert struct {
	Ticker string `json:"ticker"`
	Signal string `json:"signal"`
}

type StateWithPrice struct {
	Ticker     string
	Signal     string
	Position   float64
	Price      float64
	LastUpdate time.Time
}

var (
	mu            sync.Mutex
	defaultPairs  = []string{"BTCUSDT", "TRXUSDT", "SUIUSDT", "SOLUSDT", "NEARUSDT", "TONUSDT", "ICPUSDT"}
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
			price := getCurrentPrice(pair)
			totalCryptoValue += pos * price // Use real price for accurate value
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
			price := getCurrentPrice(alert.Ticker)
			if price == 0 {
				log.Printf("Failed to get price for %s", alert.Ticker)
				http.Error(w, "Failed to get price", http.StatusInternalServerError)
				return
			}
			size := spotBalance / price
			err = client.PlaceOrder(alert.Ticker, "buy", size)
		} else {
			// Calculate equal allocation
			targetAllocation := availableFunds / float64(buyCount+1)
			currentPos := currentState.Position
			price := getCurrentPrice(alert.Ticker)
			if price == 0 {
				log.Printf("Failed to get price for %s", alert.Ticker)
				http.Error(w, "Failed to get price", http.StatusInternalServerError)
				return
			}
			targetPos := targetAllocation / price

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

	// Update state in DB with real position size
	newPosition := positions[alert.Ticker] // Update with actual position after order
	db.UpdateState(alert.Ticker, alert.Signal, newPosition)

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

	// Get all states from DB
	states, err := db.GetAllStates()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		log.Printf("Error getting states: %v", err)
		return
	}

	// Get current positions
	client := okx.NewClient(
		os.Getenv("OKX_API_KEY"),
		os.Getenv("OKX_SECRET_KEY"),
		os.Getenv("OKX_PASSPHRASE"),
	)
	positions, err := client.GetPositions()
	if err != nil {
		http.Error(w, "Failed to get positions", http.StatusInternalServerError)
		log.Printf("Error getting positions: %v", err)
		return
	}

	// Get current prices for all tickers
	prices := getCurrentPrices(defaultPairs)

	// Combine states with prices and positions
	var statesWithPrice []StateWithPrice
	for _, state := range states {
		price := prices[state.Ticker]
		position := positions[state.Ticker] // Real position from OKX
		statesWithPrice = append(statesWithPrice, StateWithPrice{
			Ticker:     state.Ticker,
			Signal:     state.Signal,
			Position:   position,
			Price:      price,
			LastUpdate: state.LastUpdate,
		})
	}

	// Render the UI
	tmpl := template.Must(template.New("state").Parse(`
		<!DOCTYPE html>
		<html>
		<head>
			<title>Ticker States</title>
			<style>
				table { border-collapse: collapse; width: 70%; }
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
					<th>Current Price (USDT)</th>
					<th>Last Update</th>
				</tr>
				{{range .}}
				<tr>
					<td>{{.Ticker}}</td>
					<td class="{{.Signal}}">{{.Signal}}</td>
					<td>{{printf "%.4f" .Position}}</td>
					<td>{{printf "%.2f" .Price}}</td>
					<td>{{.LastUpdate.Format "2006-01-02 15:04:05"}}</td>
				</tr>
				{{end}}
			</table>
		</body>
		</html>
	`))

	err = tmpl.Execute(w, statesWithPrice)
	if err != nil {
		http.Error(w, "Failed to render state", http.StatusInternalServerError)
		log.Printf("Template error: %v", err)
	}
}

func getCurrentPrice(ticker string) float64 {
	baseURL := "https://www.okx.com"
	endpoint := "/api/v5/market/ticker"
	// Convert ticker to OKX format (e.g., BTCUSDT -> BTC-USDT)
	instId := strings.Replace(ticker, "USDT", "-USDT", 1)
	url := fmt.Sprintf("%s%s?instId=%s", baseURL, endpoint, instId)

	resp, err := http.Get(url)
	if err != nil {
		log.Printf("Error fetching price for %s: %v", ticker, err)
		return 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("OKX API error for %s: status %d", ticker, resp.StatusCode)
		return 0
	}

	var result struct {
		Data []struct {
			Last string `json:"last"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("Error decoding price for %s: %v", ticker, err)
		return 0
	}

	if len(result.Data) == 0 {
		log.Printf("No price data for %s", ticker)
		return 0
	}

	price, err := strconv.ParseFloat(result.Data[0].Last, 64)
	if err != nil {
		log.Printf("Error parsing price for %s: %v", ticker, err)
		return 0
	}
	return price
}

func getCurrentPrices(tickers []string) map[string]float64 {
	prices := make(map[string]float64)
	var wg sync.WaitGroup
	priceChan := make(chan struct {
		ticker string
		price  float64
	}, len(tickers))

	// Rate limit: OKX allows 3 requests per second per IP
	const maxConcurrent = 3
	sem := make(chan struct{}, maxConcurrent)

	for _, ticker := range tickers {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			sem <- struct{}{} // Acquire semaphore
			defer func() { <-sem }() // Release semaphore

			price := getCurrentPrice(t)
			priceChan <- struct {
				ticker string
				price  float64
			}{ticker: t, price: price}

			// Sleep to respect rate limit (3 req/s = 333ms per request)
			time.Sleep(333 * time.Millisecond)
		}(ticker)
	}

	// Collect prices
	go func() {
		wg.Wait()
		close(priceChan)
	}()

	for p := range priceChan {
		prices[p.ticker] = p.price
	}

	return prices
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