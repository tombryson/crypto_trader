package testsuite

import (
	"bytes"
	"crypto_trader/db"
	"crypto_trader/okx"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

type Alert struct {
	Ticker string `json:"ticker"`
	Signal string `json:"signal"`
}

type TestResult struct {
	Step    string
	Success bool
	Details string
}

// RunTests executes a series of predefined test scenarios
func RunTests(webhookURL string) []TestResult {
	var results []TestResult

	// Helper function to send webhook request
	sendWebhook := func(ticker, signal string) (*http.Response, error) {
		alert := Alert{Ticker: ticker, Signal: signal}
		body, err := json.Marshal(alert)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal alert: %v", err)
		}
		log.Printf("Sending webhook request: %s", string(body))
		resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(body))
		if err != nil {
			return nil, fmt.Errorf("webhook request failed: %v", err)
		}
		log.Printf("Webhook response status: %s", resp.Status)
		return resp, nil
	}

	// Helper function to get current state
	getState := func(ticker string) (db.State, float64, float64, error) {
		state, err := db.GetState(ticker)
		if err != nil {
			return db.State{}, 0, 0, fmt.Errorf("failed to get state for %s: %v", ticker, err)
		}
		positions, err := getPositions()
		if err != nil {
			return db.State{}, 0, 0, fmt.Errorf("failed to get positions: %v", err)
		}
		usdtBalance, err := getSpotBalance()
		if err != nil {
			return db.State{}, 0, 0, fmt.Errorf("failed to get USDT balance: %v", err)
		}
		position := positions[ticker]
		log.Printf("State for %s: Signal=%s, Position=%f, USDT Balance=%f", ticker, state.Signal, position, usdtBalance)
		return state, position, usdtBalance, nil
	}

	// Placeholder for fetching positions (to be implemented based on okx package)
	getPositions := func() (map[string]float64, error) {
		client := getClient()
		positions, err := client.GetPositions()
		if err != nil {
			return nil, err
		}
		log.Printf("Current positions: %v", positions)
		return positions, nil
	}

	// Placeholder for fetching spot balance (to be implemented based on okx package)
	getSpotBalance := func() (float64, error) {
		client := getClient()
		balance, err := client.GetSpotBalance()
		if err != nil {
			return 0, err
		}
		log.Printf("Current USDT balance: %f", balance)
		return balance, nil
	}

	// Placeholder for OKX client (to be implemented based on okx package)
	getClient := func() *okx.Client {
		return okx.NewClient("", "", "") // Replace with actual credentials or dependency injection
	}

	// Test 1: Buy BTC
	log.Println("=== Test 1: Buy BTC ===")
	initialStateBTC, initialPosBTC, initialBalance, err := getState("BTCUSDT")
	if err != nil {
		results = append(results, TestResult{Step: "Buy BTC - Initial State", Success: false, Details: err.Error()})
	} else {
		resp, err := sendWebhook("BTCUSDT", "buy")
		if err != nil {
			results = append(results, TestResult{Step: "Buy BTC - Webhook", Success: false, Details: err.Error()})
		} else if resp.StatusCode != http.StatusOK {
			results = append(results, TestResult{Step: "Buy BTC - Webhook Status", Success: false, Details: fmt.Sprintf("unexpected status: %s", resp.Status)})
		} else {
			// Wait for trade to process
			time.Sleep(2 * time.Second)
			newStateBTC, newPosBTC, newBalance, err := getState("BTCUSDT")
			if err != nil {
				results = append(results, TestResult{Step: "Buy BTC - Post State", Success: false, Details: err.Error()})
			} else {
				success := newStateBTC.Signal == "buy" && newPosBTC > initialPosBTC && newBalance < initialBalance
				details := fmt.Sprintf("Initial: Signal=%s, Pos=%f, Balance=%f; After: Signal=%s, Pos=%f, Balance=%f",
					initialStateBTC.Signal, initialPosBTC, initialBalance, newStateBTC.Signal, newPosBTC, newBalance)
				results = append(results, TestResult{Step: "Buy BTC", Success: success, Details: details})
			}
		}
	}

	// Test 2: Sell BTC
	log.Println("=== Test 2: Sell BTC ===")
	initialStateBTC, initialPosBTC, initialBalance, err = getState("BTCUSDT")
	if err != nil {
		results = append(results, TestResult{Step: "Sell BTC - Initial State", Success: false, Details: err.Error()})
	} else {
		resp, err := sendWebhook("BTCUSDT", "sell")
		if err != nil {
			results = append(results, TestResult{Step: "Sell BTC - Webhook", Success: false, Details: err.Error()})
		} else if resp.StatusCode != http.StatusOK {
			results = append(results, TestResult{Step: "Sell BTC - Webhook Status", Success: false, Details: fmt.Sprintf("unexpected status: %s", resp.Status)})
		} else {
			// Wait for trade to process
			time.Sleep(2 * time.Second)
			newStateBTC, newPosBTC, newBalance, err := getState("BTCUSDT")
			if err != nil {
				results = append(results, TestResult{Step: "Sell BTC - Post State", Success: false, Details: err.Error()})
			} else {
				success := newStateBTC.Signal == "sell" && newPosBTC == 0 && newBalance > initialBalance
				details := fmt.Sprintf("Initial: Signal=%s, Pos=%f, Balance=%f; After: Signal=%s, Pos=%f, Balance=%f",
					initialStateBTC.Signal, initialPosBTC, initialBalance, newStateBTC.Signal, newPosBTC, newBalance)
				results = append(results, TestResult{Step: "Sell BTC", Success: success, Details: details})
			}
		}
	}

	// Test 3: Split Funds (Buy BTC and SOL)
	log.Println("=== Test 3: Split Funds ===")
	_, _, initialBalance, err = getState("BTCUSDT")
	if err != nil {
		results = append(results, TestResult{Step: "Split Funds - Initial State", Success: false, Details: err.Error()})
	} else {
		// Buy BTC
		resp, err := sendWebhook("BTCUSDT", "buy")
		if err != nil {
			results = append(results, TestResult{Step: "Split Funds - Buy BTC Webhook", Success: false, Details: err.Error()})
		} else if resp.StatusCode != http.StatusOK {
			results = append(results, TestResult{Step: "Split Funds - Buy BTC Status", Success: false, Details: fmt.Sprintf("unexpected status: %s", resp.Status)})
		} else {
			time.Sleep(2 * time.Second)
			// Buy SOL
			resp, err = sendWebhook("SOLUSDT", "buy")
			if err != nil {
				results = append(results, TestResult{Step: "Split Funds - Buy SOL Webhook", Success: false, Details: err.Error()})
			} else if resp.StatusCode != http.StatusOK {
				results = append(results, TestResult{Step: "Split Funds - Buy SOL Status", Success: false, Details: fmt.Sprintf("unexpected status: %s", resp.Status)})
			} else {
				time.Sleep(2 * time.Second)
				_, posBTC, balanceBTC, err := getState("BTCUSDT")
				if err != nil {
					results = append(results, TestResult{Step: "Split Funds - BTC Post State", Success: false, Details: err.Error()})
				}
				_, posSOL, balanceSOL, err := getState("SOLUSDT")
				if err != nil {
					results = append(results, TestResult{Step: "Split Funds - SOL Post State", Success: false, Details: err.Error()})
				} else {
					success := posBTC > 0 && posSOL > 0 && balanceSOL < initialBalance
					details := fmt.Sprintf("Initial Balance=%f; After: BTC Pos=%f, SOL Pos=%f, Balance=%f",
						initialBalance, posBTC, posSOL, balanceSOL)
					results = append(results, TestResult{Step: "Split Funds", Success: success, Details: details})
				}
			}
		}
	}

	// Test 4: Full Cycle (Buy and Sell Multiple Tickers)
	log.Println("=== Test 4: Full Cycle ===")
	_, _, initialBalance, err = getState("BTCUSDT")
	if err != nil {
		results = append(results, TestResult{Step: "Full Cycle - Initial State", Success: false, Details: err.Error()})
	} else {
		// Buy BTC
		sendWebhook("BTCUSDT", "buy")
		time.Sleep(2 * time.Second)
		// Buy SOL
		sendWebhook("SOLUSDT", "buy")
		time.Sleep(2 * time.Second)
		// Sell BTC
		sendWebhook("BTCUSDT", "sell")
		time.Sleep(2 * time.Second)
		// Sell SOL
		sendWebhook("SOLUSDT", "sell")
		time.Sleep(2 * time.Second)
		_, posBTC, _, err := getState("BTCUSDT")
		if err != nil {
			results = append(results, TestResult{Step: "Full Cycle - BTC Post State", Success: false, Details: err.Error()})
		}
		_, posSOL, finalBalance, err := getState("SOLUSDT")
		if err != nil {
			results = append(results, TestResult{Step: "Full Cycle - SOL Post State", Success: false, Details: err.Error()})
		} else {
			success := posBTC == 0 && posSOL == 0
			details := fmt.Sprintf("Initial Balance=%f; Final: BTC Pos=%f, SOL Pos=%f, Balance=%f",
				initialBalance, posBTC, posSOL, finalBalance)
			results = append(results, TestResult{Step: "Full Cycle", Success: success, Details: details})
		}
	}

	// Test 5: Error Handling (Invalid Ticker)
	log.Println("=== Test 5: Error Handling ===")
	resp, err := sendWebhook("INVALIDUSDT", "buy")
	if err != nil {
		results = append(results, TestResult{Step: "Error Handling - Webhook", Success: false, Details: err.Error()})
	} else if resp.StatusCode != http.StatusOK {
		success := resp.StatusCode == http.StatusInternalServerError || resp.StatusCode == http.StatusBadRequest
		details := fmt.Sprintf("Expected error status, got: %s", resp.Status)
		results = append(results, TestResult{Step: "Error Handling", Success: success, Details: details})
	} else {
		results = append(results, TestResult{Step: "Error Handling", Success: false, Details: "Expected error, but request succeeded"})
	}

	return results
}