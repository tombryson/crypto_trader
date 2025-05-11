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
	"crypto_trader/testsuite"
	"io/ioutil"
)

type Alert struct {
	Ticker string `json:"ticker"`
	Signal string `json:"signal"`
}

type StateWithPrice struct {
	Ticker      string
	Signal      string
	Position    float64
	Price       float64
	PositionValue float64 // Value of the position in USDT
	LastUpdate  time.Time
	USDTBalance float64
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

type OrderResponse struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []struct {
		OrdId     string `json:"ordId"`
		InstId    string `json:"instId"`
		State     string `json:"state"`
	} `json:"data"`
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
	

	// Get current price
	price := getCurrentPrice(alert.Ticker)
	if price == 0 {
		log.Printf("Failed to get price for %s", alert.Ticker)
		http.Error(w, "Failed to get price", http.StatusInternalServerError)
		return
	}

	// Calculate total allocated and available funds
	var totalCryptoValue float64
	for _, pair := range defaultPairs {
		if pos, ok := positions[pair]; ok {
			price := getCurrentPrice(pair)
			totalCryptoValue += pos * price
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

	var size float64
	var orderId string
	if alert.Signal == "buy" {
		if buyCount == 0 {
			// First buy signal, allocate all spot funds
			size = spotBalance / price
		} else {
			// Calculate equal allocation
			targetAllocation := availableFunds / float64(buyCount+1)
			currentPos := currentState.Position
			targetPos := targetAllocation / price

			if currentPos > 0 {
				// Sell excess if needed
				if currentPos > targetPos {
					size = currentPos - targetPos
					err = client.PlaceOrder(alert.Ticker, "sell", size)
					if err == nil {
						orderId, err = checkOrderStatus(client, alert.Ticker, "sell", size)
						if err == nil && orderId != "" {
							usdtValue := size * price
							db.RecordTransaction(alert.Ticker, "sell", size, price, usdtValue)
							log.Printf("Sell order %s for %s confirmed, size %f", orderId, alert.Ticker, size)
						} else {
							log.Printf("Sell order for %s failed or not filled: %v", alert.Ticker, err)
						}
					}
				}
			} else {
				// Buy new position
				size = targetPos
			}
		}
		if size > 0 {
			err = client.PlaceOrder(alert.Ticker, "buy", size)
			if err == nil {
				orderId, err = checkOrderStatus(client, alert.Ticker, "buy", size)
				if err == nil && orderId != "" {
					usdtValue := size * price
					db.RecordTransaction(alert.Ticker, "buy", size, price, usdtValue)
					log.Printf("Buy order %s for %s confirmed, size %f", orderId, alert.Ticker, size)
				} else {
					log.Printf("Buy order for %s failed or not filled: %v", alert.Ticker, err)
				}
			}
		}
	} else if alert.Signal == "sell" {
		if currentState.Position > 0 {
			size = currentState.Position
			err = client.PlaceOrder(alert.Ticker, "sell", size)
			if err == nil {
				orderId, err = checkOrderStatus(client, alert.Ticker, "sell", size)
				if err == nil && orderId != "" {
					usdtValue := size * price
					db.RecordTransaction(alert.Ticker, "sell", size, price, usdtValue)
					log.Printf("Sell order %s for %s confirmed, size %f", orderId, alert.Ticker, size)
				} else {
					log.Printf("Sell order for %s failed or not filled: %v", alert.Ticker, err)
				}
			}
		}
	}

	if err != nil {
		log.Printf("Error placing order: %v", err)
		http.Error(w, "Failed to place order", http.StatusInternalServerError)
		return
	}

	// Update state in DB with real position size
	newPosition := positions[alert.Ticker]
	db.UpdateState(alert.Ticker, alert.Signal, newPosition)

	// Record account value
	totalAccountValue := spotBalance
	for _, pair := range defaultPairs {
		if pos, ok := positions[pair]; ok {
			totalAccountValue += pos * getCurrentPrice(pair)
		}
	}
	db.RecordAccountValue(totalAccountValue)

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Alert processed: %s %s", alert.Ticker, alert.Signal)
}

func checkOrderStatus(client *okx.Client, ticker, side string, size float64) (string, error) {
	endpoint := "/api/v5/trade/order"
	instId := strings.Replace(ticker, "USDT", "-USDT", 1)

	// Place the order and assume PlaceOrder handles the request
	orderParams := map[string]string{
		"instId":  instId,
		"tdMode":  "cash",
		"side":    side,
		"ordType": "market",
		"sz":      fmt.Sprintf("%.2f", size),
	}
	resp, err := client.PlaceOrderWithResponse(alert.Ticker, side, size) // Assuming this returns the response
	if err != nil {
		return "", err
	}

	// Parse the response to get the order ID
	var orderResp OrderResponse
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if err := json.Unmarshal(body, &orderResp); err != nil {
		return "", err
	}

	if orderResp.Code != "0" {
		return "", fmt.Errorf("OKX API error: %s", orderResp.Msg)
	}

	if len(orderResp.Data) == 0 {
		return "", fmt.Errorf("no order data returned")
	}

	orderId := orderResp.Data[0].OrdId
	log.Printf("Order placed, ID: %s, InstId: %s, State: %s", orderId, instId, orderResp.Data[0].State)

	// Poll until the order is filled or timed out
	timeout := time.After(10 * time.Second)
	tickerPoll := time.NewTicker(1 * time.Second)
	defer tickerPoll.Stop()

	for {
		select {
		case <-timeout:
			return "", fmt.Errorf("order %s timed out", orderId)
		case <-tickerPoll.C:
			// Create a new request to check order status
			params := map[string]string{
				"instId": instId,
				"ordId":  orderId,
			}
			resp, err := client.GetOrderStatus(instId, orderId) // Assuming this method exists
			if err != nil {
				return "", err
			}

			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()

			var statusResp OrderResponse
			if err := json.Unmarshal(body, &statusResp); err != nil {
				return "", err
			}

			if statusResp.Code != "0" {
				return "", fmt.Errorf("OKX API error checking status: %s", statusResp.Msg)
			}

			if len(statusResp.Data) == 0 {
				continue
			}

			state := statusResp.Data[0].State
			log.Printf("Order %s status: %s", orderId, state)
			if state == "filled" {
				return orderId, nil
			}
		}
	}
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

	// Get USDT balance
	usdtBalance, err := client.GetSpotBalance()
	if err != nil {
		http.Error(w, "Failed to get USDT balance", http.StatusInternalServerError)
		log.Printf("Error getting USDT balance: %v", err)
		return
	}

	// Get current prices for all tickers
	prices := getCurrentPrices(defaultPairs)

	// Combine states with prices, positions, and USDT balance
	var statesWithPrice []StateWithPrice
	var totalAccountValue float64 = usdtBalance
	for _, state := range states {
		price := prices[state.Ticker]
		position := positions[state.Ticker]
		positionValue := position * price
		totalAccountValue += positionValue
		statesWithPrice = append(statesWithPrice, StateWithPrice{
			Ticker:      state.Ticker,
			Signal:      state.Signal,
			Position:    position,
			Price:       price,
			PositionValue: positionValue,
			LastUpdate:  state.LastUpdate,
			USDTBalance: usdtBalance,
		})
	}

	// Get historical account values
	accountValues, err := db.GetAccountValues()
	if err != nil {
		log.Printf("Error getting account values: %v", err)
	}

	// Calculate percentage gain/loss per ticker
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
					<td>{{printf "%.4f" .Position}}</td>
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
		States           []StateWithPrice
		TotalAccountValue float64
		AccountValues    []struct{ TotalUSDT float64; Timestamp time.Time }
		PairPerformance  map[string]float64
	}{
		States:           statesWithPrice,
		TotalAccountValue: totalAccountValue,
		AccountValues:    accountValues,
		PairPerformance:  pairPerformance,
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
	results := testsuite.RunTests("https://crypto-trader15-delicate-flower-4267.fly.dev/webhook")

	// Render test results
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintln(w, "Test Suite Results:")
	for _, result := range results {
		status := "PASS"
		if !result.Success {
			status = "FAIL"
		}
		fmt.Fprintf(w, "[%s] %s: %s\n", status, result.Step, result.Details)
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
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response for %s: %v", ticker, err)
		return 0
	}
	if err := json.Unmarshal(body, &result); err != nil {
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

	http.HandleFunc("/webhook", handler)
	http.HandleFunc("/state", stateHandler)
	http.HandleFunc("/run-tests", testHandler)
	port := ":8080"
	log.Printf("Server starting on port %s...", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}