package main

import (
	"crypto_trader/db"
	"crypto_trader/okx"
	crypto_trader "crypto_trader/testsuite"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Alert struct {
	Ticker string `json:"ticker"`
	Signal string `json:"signal"`
}

type StateWithPrice struct {
	Ticker        string
	Signal        string
	Position      float64
	Price         float64
	PositionValue float64 // Value of the position in USDT
	LastUpdate    time.Time
	USDTBalance   float64
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

var (
	mu           sync.Mutex
	defaultPairs = []string{"BTCUSDT", "TRXUSDT", "SUIUSDT", "SOLUSDT", "NEARUSDT", "TONUSDT", "ICPUSDT"}
	lotSizes     = make(map[string]float64)
)

func fetchLotSizes() error {
	baseURL := "https://www.okx.com"
	endpoint := "/api/v5/public/instruments?instType=SPOT"
	url := fmt.Sprintf("%s%s", baseURL, endpoint)

	for retries := 0; retries < 3; retries++ {
		resp, err := http.Get(url)
		if err != nil {
			log.Printf("Retrying instruments fetch (%d/3): %v", retries+1, err)
			time.Sleep(time.Second * time.Duration(retries+1))
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			log.Printf("Retrying instruments fetch (%d/3): status %d, body %s", retries+1, resp.StatusCode, string(body))
			time.Sleep(time.Second * time.Duration(retries+1))
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("error reading instrument response: %v", err)
		}
		log.Printf("OKX instruments response for SPOT: %s", string(bodyBytes))

		var result struct {
			Data []struct {
				InstId string `json:"instId"`
				LotSz  string `json:"lotSz"`
			} `json:"data"`
		}
		if err := json.Unmarshal(bodyBytes, &result); err != nil {
			return fmt.Errorf("error decoding instrument details: %v", err)
		}

		for _, inst := range result.Data {
			ticker := strings.Replace(inst.InstId, "-USDT", "USDT", 1)
			if contains(defaultPairs, ticker) {
				lotSz, err := strconv.ParseFloat(inst.LotSz, 64)
				if err != nil {
					log.Printf("Error parsing lotSz for %s: %v", ticker, err)
					continue
				}
				// Validate lot size (should be reasonable, e.g., >= 0.0001)
				if lotSz < 0.0001 || lotSz > 1 {
					log.Printf("Invalid lotSz for %s: %f, skipping", ticker, lotSz)
					continue
				}
				lotSizes[ticker] = lotSz
				log.Printf("Fetched lotSz: %f for %s", lotSz, ticker)
			}
		}
		return nil
	}
	return fmt.Errorf("failed to fetch instruments after 3 retries")
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var alert Alert
	if err := json.NewDecoder(r.Body).Decode(&alert); err != nil {
		log.Printf("Invalid JSON payload: %v", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	log.Printf("Received alert: Ticker=%s, Signal=%s", alert.Ticker, alert.Signal)

	if !isValidTicker(alert.Ticker) {
		log.Printf("Invalid ticker: %s", alert.Ticker)
		http.Error(w, "Invalid ticker", http.StatusBadRequest)
		return
	}

	client := okx.NewClient(
		os.Getenv("OKX_API_KEY"),
		os.Getenv("OKX_SECRET_KEY"),
		os.Getenv("OKX_PASSPHRASE"),
	)

	mu.Lock()
	defer mu.Unlock()

	currentState, err := db.GetState(alert.Ticker)
	if err != nil {
		log.Printf("Error getting state for %s: %v", alert.Ticker, err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	log.Printf("Retrieved state for %s: Signal=%s, Position=%f, LastUpdate=%s",
		alert.Ticker, currentState.Signal, currentState.Position, currentState.LastUpdate)

	if currentState.Signal == alert.Signal {
		log.Printf("Ticker %s already in %s state, skipping order (to force order, remove this check for testing)",
			alert.Ticker, alert.Signal)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Alert processed (no action): %s %s", alert.Ticker, alert.Signal)
		return
	}

	spotBalance, err := client.GetSpotBalance()
	if err != nil {
		log.Printf("Error getting available spot balance: %v", err)
		http.Error(w, "Failed to get balance", http.StatusInternalServerError)
		return
	}
	log.Printf("Available spot balance: %.2f USDT", spotBalance)
	if spotBalance < 28.77 {
		log.Printf("Insufficient available balance: %.2f USDT, need 28.77 USDT", spotBalance)
		http.Error(w, "Insufficient available balance", http.StatusInternalServerError)
		return
	}

	hasOpenOrders, err := client.GetOpenOrders(alert.Ticker)
	if err != nil {
		log.Printf("Error checking open orders for %s: %v", alert.Ticker, err)
		http.Error(w, "Failed to check open orders", http.StatusInternalServerError)
		return
	}
	if hasOpenOrders {
		log.Printf("Open orders exist for %s, cannot place new order", alert.Ticker)
		http.Error(w, "Open orders exist", http.StatusInternalServerError)
		return
	}

	positions, err := client.GetPositions()
	if err != nil {
		log.Printf("Error getting positions: %v", err)
		http.Error(w, "Failed to get positions", http.StatusInternalServerError)
		return
	}
	log.Printf("Current positions: %v", positions)

	price := getCurrentPrice(alert.Ticker)
	if price == 0 {
		log.Printf("Failed to get price for %s", alert.Ticker)
		http.Error(w, "Failed to get price", http.StatusInternalServerError)
		return
	}
	log.Printf("Current price for %s: %f", alert.Ticker, price)

	var totalCryptoValue float64
	for _, pair := range defaultPairs {
		if pos, ok := positions[pair]; ok {
			price := getCurrentPrice(pair)
			totalCryptoValue += pos * price
		}
	}
	availableFunds := spotBalance + totalCryptoValue
	log.Printf("Total crypto value: %f, Available funds: %f", totalCryptoValue, availableFunds)

	buyCount := 0
	for _, pair := range defaultPairs {
		state, _ := db.GetState(pair)
		if state.Signal == "buy" {
			buyCount++
		}
	}
	log.Printf("Number of buy signals: %d", buyCount)

	var size float64
	var orderPlaced bool
	lotSize, exists := lotSizes[alert.Ticker]
	if !exists || lotSize < 0.0001 || lotSize > 1 {
		log.Printf("Invalid or missing lot size for %s (%f), using default 0.1", alert.Ticker, lotSize)
		lotSize = 0.1 // Fallback for TRXUSDT
	}
	log.Printf("Using lot size for %s: %f", alert.Ticker, lotSize)

	if alert.Signal == "buy" {
		if buyCount == 0 {
			size = spotBalance / price
			log.Printf("First buy signal for %s, allocating all funds: size=%f", alert.Ticker, size)
		} else {
			targetAllocation := availableFunds / float64(buyCount+1)
			currentPos := currentState.Position
			targetPos := targetAllocation / price
			log.Printf("Target allocation for %s: %f, Target position: %f", alert.Ticker, targetAllocation, targetPos)

			if currentPos > 0 {
				if currentPos > targetPos {
					size = currentPos - targetPos
					log.Printf("Selling excess for %s: size=%f", alert.Ticker, size)
					err = client.PlaceOrder(alert.Ticker, "sell", size, lotSize)
					if err == nil {
						usdtValue := size * price
						db.RecordTransaction(alert.Ticker, "sell", size, price, usdtValue)
						log.Printf("Sell order placed for %s, size=%f, USDT value=%f", alert.Ticker, size, usdtValue)
						orderPlaced = true
					}
				}
			} else {
				size = targetPos
				log.Printf("Buying new position for %s: size=%f", alert.Ticker, size)
			}
		}

		// Enforce minimum order value of 10 USDT
		minOrderValueUSDT := 10.0
		minSizeForValue := minOrderValueUSDT / price
		if size < minSizeForValue {
			log.Printf("Adjusted size for %s from %.8f to %.8f to meet minimum order value of %.2f USDT", alert.Ticker, size, minSizeForValue, minOrderValueUSDT)
			size = minSizeForValue
		}

		// Round size to lot size precision
		if lotSize > 0 {
			size = float64(int(size/lotSize)) * lotSize
			log.Printf("Rounded size for %s to %.8f (multiple of lotSize %f)", alert.Ticker, size, lotSize)
		}

		// Check if we have enough funds for the adjusted size
		usdtValue := size * price
		if usdtValue > spotBalance {
			log.Printf("Insufficient funds for %s: need %.2f USDT, have %.2f USDT", alert.Ticker, usdtValue, spotBalance)
			http.Error(w, "Insufficient funds", http.StatusInternalServerError)
			return
		}

		// Validate size is a multiple of lotSize
		if lotSize > 0 && math.Abs(math.Mod(size/lotSize, 1)) > 1e-10 {
			log.Printf("Invalid size for %s: %f is not a multiple of lotSize %f", alert.Ticker, size, lotSize)
			http.Error(w, "Invalid order size", http.StatusInternalServerError)
			return
		}

		if size > 0 {
			log.Printf("Attempting to place buy order for %s with size %.8f", alert.Ticker, size)
			err = client.PlaceOrder(alert.Ticker, "buy", size, lotSize)
			if err == nil {
				usdtValue := size * price
				db.RecordTransaction(alert.Ticker, "buy", size, price, usdtValue)
				log.Printf("Buy order placed for %s, size=%.8f, USDT value=%.2f", alert.Ticker, size, usdtValue)
				orderPlaced = true
			} else {
				log.Printf("Failed to place buy order for %s: %v", alert.Ticker, err)
			}
		}

	} else if alert.Signal == "sell" {
		if currentState.Position > 0 {
			size = currentState.Position
			// Enforce minimum order value of 10 USDT
			minOrderValueUSDT := 10.0
			minSizeForValue := minOrderValueUSDT / price
			if size < minSizeForValue {
				log.Printf("Adjusted size for %s from %.8f to %.8f to meet minimum order value of %.2f USDT", alert.Ticker, size, minSizeForValue, minOrderValueUSDT)
				size = minSizeForValue
			}
			// Round size to lot size precision
			if lotSize > 0 {
				size = float64(int(size/lotSize)) * lotSize
				log.Printf("Rounded size for %s to %.8f (multiple of lotSize %f)", alert.Ticker, size, lotSize)
			}
			// Validate size is a multiple of lotSize
			if lotSize > 0 && math.Abs(math.Mod(size/lotSize, 1)) > 1e-10 {
				log.Printf("Invalid size for %s: %f is not a multiple of lotSize %f", alert.Ticker, size, lotSize)
				http.Error(w, "Invalid order size", http.StatusInternalServerError)
				return
			}
			log.Printf("Selling entire position for %s: size=%.8f", alert.Ticker, size)
			err = client.PlaceOrder(alert.Ticker, "sell", size, lotSize)
			if err == nil {
				usdtValue := size * price
				db.RecordTransaction(alert.Ticker, "sell", size, price, usdtValue)
				log.Printf("Sell order placed for %s, size=%.8f, USDT value=%.2f", alert.Ticker, size, usdtValue)
				orderPlaced = true
			} else {
				log.Printf("Failed to place sell order for %s: %v", alert.Ticker, err)
			}
		}
	}

	if err != nil {
		log.Printf("Error placing order for %s: %v", alert.Ticker, err)
		http.Error(w, "Failed to place order", http.StatusInternalServerError)
		return
	}

	if orderPlaced {
		time.Sleep(2 * time.Second)
		positions, err = client.GetPositions()
		if err != nil {
			log.Printf("Error updating positions after order: %v", err)
		} else {
			newPosition := positions[alert.Ticker]
			db.UpdateState(alert.Ticker, alert.Signal, newPosition)
			log.Printf("Updated state for %s: Signal=%s, Position=%.8f", alert.Ticker, alert.Signal, newPosition)
		}

		spotBalance, err = client.GetSpotBalance()
		if err != nil {
			log.Printf("Error getting spot balance after order: %v", err)
		}
		totalAccountValue := spotBalance
		for _, pair := range defaultPairs {
			if pos, ok := positions[pair]; ok {
				totalAccountValue += pos * getCurrentPrice(pair)
			}
		}
		db.RecordAccountValue(totalAccountValue)
		log.Printf("Recorded total account value: %f", totalAccountValue)
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Alert processed: %s %s", alert.Ticker, alert.Signal)
}

func isValidTicker(ticker string) bool {
	for _, pair := range defaultPairs {
		if pair == ticker {
			return true
		}
	}
	return false
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

	usdtBalance, err := client.GetSpotBalance()
	if err != nil {
		http.Error(w, "Failed to get USDT balance", http.StatusInternalServerError)
		log.Printf("Error getting USDT balance: %v", err)
		return
	}

	prices := getCurrentPrices(defaultPairs)

	var statesWithPrice []StateWithPrice
	var totalAccountValue float64 = usdtBalance
	for _, state := range states {
		price := prices[state.Ticker]
		position := positions[state.Ticker]
		positionValue := position * price
		totalAccountValue += positionValue
		statesWithPrice = append(statesWithPrice, StateWithPrice{
			Ticker:        state.Ticker,
			Signal:        state.Signal,
			Position:      position,
			Price:         price,
			PositionValue: positionValue,
			LastUpdate:    state.LastUpdate,
			USDTBalance:   usdtBalance,
		})
	}

	accountValues, err := db.GetAccountValues()
	if err != nil {
		log.Printf("Error getting account values: %v", err)
	}

	pairPerformance := make(map[string]float64)
	for _, ticker := range defaultPairs {
		transactions, err := db.GetTransactions(ticker)
		if err != nil {
			log.Printf("Error getting transactions for %s: %v", ticker, err)
			continue
		}
		var totalBuyUSDT, totalSellUSDT float64
		for _, t := range transactions {
			if t.Signal == "buy" {
				totalBuyUSDT += t.USDTValue
			} else if t.Signal == "sell" {
				totalSellUSDT += t.USDTValue
			}
		}
		if totalBuyUSDT > 0 {
			pairPerformance[ticker] = ((totalSellUSDT - totalBuyUSDT) / totalBuyUSDT) * 100
		} else {
			pairPerformance[ticker] = 0
		}
	}

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
				.chart-container { width: 70%; height: 400px; }
			</style>
			<script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
		</head>
		<body>
			<h1>Ticker States</h1>
			<p>USDT Balance: {{printf "%.2f" (index .States 0).USDTBalance}}</p>
			<p>Total Account Value (USDT): {{printf "%.2f" .TotalAccountValue}}</p>
			<div class="chart-container">
				<canvas id="accountValueChart"></canvas>
			</div>
			<table>
				<tr>
					<th>Ticker</th>
					<th>Signal</th>
					<th>Position</th>
					<th>Value (USDT)</th>
					<th>Current Price (USDT)</th>
					<th>% Gain/Loss</th>
					<th>Last Update</th>
				</tr>
				{{range .States}}
				<tr>
					<td>{{.Ticker}}</td>
					<td class="{{.Signal}}">{{.Signal}}</td>
					<td>{{printf "%.8f" .Position}}</td>
					<td>{{printf "%.2f" .PositionValue}}</td>
					<td>{{printf "%.2f" .Price}}</td>
					<td>{{printf "%.2f" (index $.PairPerformance .Ticker)}}%</td>
					<td>{{.LastUpdate.Format "2006-01-02 15:04:05"}}</td>
				</tr>
				{{end}}
			</table>
			<script>
				const ctx = document.getElementById('accountValueChart').getContext('2d');
				const accountValueChart = new Chart(ctx, {
					type: 'line',
					data: {
						labels: {{range .AccountValues}}{{.Timestamp.Format "2006-01-02 15:04:05" | printf "'%s'"}},{{end}},
						datasets: [{
							label: 'Total Account Value (USDT)',
							data: {{range .AccountValues}}{{printf "%.2f" .TotalUSDT}},{{end}},
							borderColor: 'rgb(75, 192, 192)',
							tension: 0.1
						}]
					},
					options: {
						scales: {
							y: { beginAtZero: false }
						}
					}
				});
			</script>
		</body>
		</html>
	`))

	data := struct {
		States            []StateWithPrice
		TotalAccountValue float64
		AccountValues     []struct{ TotalUSDT float64; Timestamp time.Time }
		PairPerformance   map[string]float64
	}{
		States:            statesWithPrice,
		TotalAccountValue: totalAccountValue,
		AccountValues:     accountValues,
		PairPerformance:   pairPerformance,
	}

	err = tmpl.Execute(w, data)
	if err != nil {
		http.Error(w, "Failed to render state", http.StatusInternalServerError)
		log.Printf("Template error: %v", err)
	}
}

func testHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	log.Println("Starting test suite...")
	client := okx.NewClient(
		os.Getenv("OKX_API_KEY"),
		os.Getenv("OKX_SECRET_KEY"),
		os.Getenv("OKX_PASSPHRASE"),
	)

	results := crypto_trader.RunTests("https://crypto-trader15-delicate-flower-4267.fly.dev/webhook", client)

	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintln(w, "Test Suite Results:")
	for _, result := range results {
		status := "PASS"
		if !result.Success {
			status = "FAIL"
		}
		fmt.Fprintf(w, "[%,environm] %s: %s\n", status, result.Step, result.Details)
	}
	log.Println("Test suite completed.")
}

func getCurrentPrice(ticker string) float64 {
	baseURL := "https://www.okx.com"
	endpoint := "/api/v5/market/ticker"
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

	const maxConcurrent = 3
	sem := make(chan struct{}, maxConcurrent)

	for _, ticker := range tickers {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			price := getCurrentPrice(t)
			priceChan <- struct {
				ticker string
				price  float64
			}{ticker: t, price: price}

			time.Sleep(333 * time.Millisecond)
		}(ticker)
	}

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

	if err := fetchLotSizes(); err != nil {
		log.Fatalf("Failed to fetch lot sizes: %v", err)
	}

	// Set default lot sizes for missing or invalid pairs
	defaultLotSizes := map[string]float64{
		"BTCUSDT":  0.000001,
		"TRXUSDT":  0.1,
		"SUIUSDT":  0.0001,
		"SOLUSDT":  0.0001,
		"NEARUSDT": 0.0001,
		"TONUSDT":  0.0001,
		"ICPUSDT":  0.0001,
	}
	for _, ticker := range defaultPairs {
		if _, exists := lotSizes[ticker]; !exists {
			lotSizes[ticker] = defaultLotSizes[ticker]
			log.Printf("No lotSz for %s, setting default: %f", ticker, defaultLotSizes[ticker])
		}
	}

	if len(lotSizes) != len(defaultPairs) {
		log.Printf("Warning: fetched lot sizes for %d/%d pairs", len(lotSizes), len(defaultPairs))
	}

	http.HandleFunc("/webhook", handler)
	http.HandleFunc("/state", stateHandler)
	http.HandleFunc("/run-tests", testHandler)
	port := ":8080"
	log.Printf("Server starting on port %s...", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}