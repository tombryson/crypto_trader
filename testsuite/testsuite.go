package crypto_trader

import (
	"bytes"
	"crypto_trader/db"
	"crypto_trader/okx"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type TestResult struct {
	Step    string
	Success bool
	Details string
}

func RunTests(webhookURL string, client *okx.Client) []TestResult {
	var results []TestResult

	// Test 1: Buy TRX (Simulated TradingView Buy Signal)
	log.Println("=== Test 1: Buy TRX ===")
	results = append(results, TestResult{Step: "Buy TRX", Success: true, Details: "Starting test"})

	db.ResetState("TRXUSDT", "sell", 0.0)

	// Simulate TradingView buy signal
	payload := []byte(`{"ticker":"TRXUSDT","signal":"buy"}`)
	log.Printf("Simulating TradingView buy signal: %s", payload)

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(payload))
	if err != nil {
		log.Printf("Error creating webhook request: %v", err)
		results[0].Success = false
		results[0].Details = fmt.Sprintf("Failed to create webhook request: %v", err)
		return results
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Error sending webhook request: %v", err)
		results[0].Success = false
		results[0].Details = fmt.Sprintf("Failed to send webhook request: %v", err)
		return results
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Webhook response status: %s, body: %s", resp.Status, string(body))
		results[0].Success = false
		results[0].Details = fmt.Sprintf("Webhook failed with status: %s, body: %s", resp.Status, string(body))
		return results
	}

	// Wait for the order to process
	time.Sleep(3 * time.Second)

	// Check position
	positions, err := client.GetPositions()
	if err != nil {
		log.Printf("Error checking positions: %v", err)
		results[0].Success = false
		results[0].Details = fmt.Sprintf("Failed to check positions: %v", err)
		return results
	}

	trxPosition := positions["TRXUSDT"]
	if trxPosition <= 0 {
		log.Printf("TRX position after buy: %.8f", trxPosition)
		results[0].Success = false
		results[0].Details = fmt.Sprintf("Position not updated, got %.8f", trxPosition)
		return results
	}

	price := getCurrentPrice("TRXUSDT")
	if price == 0 {
		results[0].Success = false
		results[0].Details = "Failed to get current price"
		return results
	}
	expectedSize := 10.0 / price
	if trxPosition < expectedSize*0.9 || trxPosition > expectedSize*1.1 { // Allow 10% tolerance due to price fluctuations
		log.Printf("TRX position %.8f deviates from expected %.8f", trxPosition, expectedSize)
		results[0].Success = false
		results[0].Details = fmt.Sprintf("Position %.8f out of range of expected %.8f", trxPosition, expectedSize)
		return results
	}

	log.Printf("TRX position after buy: %.8f (expected ~%.8f)", trxPosition, expectedSize)
	results[0].Details = fmt.Sprintf("Successfully bought %.8f TRX", trxPosition)

	// Test 2: Sell TRX (Simulated TradingView Sell Signal)
	log.Println("=== Test 2: Sell TRX ===")
	results = append(results, TestResult{Step: "Sell TRX", Success: true, Details: "Starting test"})

	payload = []byte(`{"ticker":"TRXUSDT","signal":"sell"}`)
	log.Printf("Simulating TradingView sell signal: %s", payload)

	req, err = http.NewRequest("POST", webhookURL, bytes.NewBuffer(payload))
	if err != nil {
		log.Printf("Error creating webhook request: %v", err)
		results[1].Success = false
		results[1].Details = fmt.Sprintf("Failed to create webhook request: %v", err)
		return results
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Error sending webhook request: %v", err)
		results[1].Success = false
		results[1].Details = fmt.Sprintf("Failed to send webhook request: %v", err)
		return results
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Webhook response status: %s, body: %s", resp.Status, string(body))
		results[1].Success = false
		results[1].Details = fmt.Sprintf("Webhook failed with status: %s, body: %s", resp.Status, string(body))
		return results
	}

	// Wait for the order to process
	time.Sleep(3 * time.Second)

	// Check position
	positions, err = client.GetPositions()
	if err != nil {
		log.Printf("Error checking positions: %v", err)
		results[1].Success = false
		results[1].Details = fmt.Sprintf("Failed to check positions: %v", err)
		return results
	}

	trxPosition = positions["TRXUSDT"]
	if trxPosition != 0 {
		log.Printf("TRX position after sell: %.8f", trxPosition)
		results[1].Success = false
		results[1].Details = fmt.Sprintf("Position not zero, got %.8f", trxPosition)
		return results
	}

	log.Printf("TRX position after sell: %.8f", trxPosition)
	results[1].Details = fmt.Sprintf("Successfully sold all TRX")

	log.Println("Test suite completed.")
	return results
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