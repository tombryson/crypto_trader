package okx

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func NewClient(apiKey, secretKey, passphrase string) *Client {
	return &Client{
		APIKey:    apiKey,
		SecretKey: secretKey,
		Passphrase: passphrase,
	}
}

func (c *Client) signRequest(timestamp, method, requestPath, body string) string {
	message := fmt.Sprintf("%s%s%s%s", timestamp, strings.ToUpper(method), requestPath, body)
	mac := hmac.New(sha256.New, []byte(c.SecretKey))
	mac.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (c *Client) PlaceOrder(ticker, signal string, size float64) error {
	baseURL := "https://www.okx.com"
	endpoint := "/api/v5/trade/order"
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	// Convert ticker to OKX format (e.g., BTCUSDT -> BTC-USDT)
	instrumentID := strings.Replace(strings.ToUpper(ticker), "USDT", "-USDT", 1)

	var side string
	if signal == "buy" {
		side = "buy"
	} else if signal == "sell" {
		side = "sell"
	} else {
		return fmt.Errorf("invalid signal: %s", signal)
	}

	order := map[string]string{
		"instId":  instrumentID,
		"tdMode":  "cash",
		"side":    side,
		"ordType": "market",
		"sz":      fmt.Sprintf("%.2f", size),
	}

	bodyBytes, _ := json.Marshal(order)
	body := string(bodyBytes)
	if body == "{}" {
		body = ""
	}

	signature := c.signRequest(timestamp, "POST", endpoint, body)

	req, err := http.NewRequest("POST", baseURL+endpoint, bytes.NewBuffer([]byte(body)))
	if err != nil {
		return err
	}

	req.Header.Set("OK-ACCESS-KEY", c.APIKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.Passphrase)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("OKX API error: %s, status: %d", string(bodyBytes), resp.StatusCode)
	}

	log.Printf("Order placed successfully for %s with signal %s, size %.2f", ticker, signal, size)
	return nil
}

func (c *Client) GetSpotBalance() (float64, error) {
	baseURL := "https://www.okx.com"
	endpoint := "/api/v5/account/balance"
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	body := ""

	signature := c.signRequest(timestamp, "GET", endpoint, body)

	req, err := http.NewRequest("GET", baseURL+endpoint+"?ccy=USDT", nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("OK-ACCESS-KEY", c.APIKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.Passphrase)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("OKX API error: %s, status: %d", string(bodyBytes), resp.StatusCode)
	}

	var result struct {
		Data []struct {
			Details []struct {
				CashBal string `json:"cashBal"`
			} `json:"details"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	if len(result.Data) > 0 && len(result.Data[0].Details) > 0 {
		balance, err := strconv.ParseFloat(result.Data[0].Details[0].CashBal, 64)
		if err != nil {
			return 0, err
		}
		return balance, nil
	}
	return 0, fmt.Errorf("no USDT balance found")
}

func (c *Client) GetPositions() (map[string]float64, error) {
	// Use /api/v5/asset/balances to get spot wallet balances
	baseURL := "https://www.okx.com"
	endpoint := "/api/v5/asset/balances"
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	body := ""

	signature := c.signRequest(timestamp, "GET", endpoint, body)

	req, err := http.NewRequest("GET", baseURL+endpoint, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("OK-ACCESS-KEY", c.APIKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.Passphrase)
	req.Header.Set("Content-Type", "application/json")

	log.Printf("Sending balances request to %s", req.URL.String())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("OKX API response body: %s", string(bodyBytes))
		return nil, fmt.Errorf("OKX API error: %s, status: %d", string(bodyBytes), resp.StatusCode)
	}

	var result struct {
		Data []struct {
			Ccy   string `json:"ccy"`   // Currency, e.g., "BTC"
			Bal   string `json:"bal"`   // Balance
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	positions := make(map[string]float64)
	// Map currencies to tickers (e.g., "BTC" to "BTCUSDT")
	for _, asset := range result.Data {
		ticker := strings.ToUpper(asset.Ccy + "USDT")
		balance, err := strconv.ParseFloat(asset.Bal, 64)
		if err != nil {
			log.Printf("Error parsing balance for %s: %v", asset.Ccy, err)
			continue
		}
		positions[ticker] = balance
	}

	// Ensure all tickers in defaultPairs have an entry, even if balance is 0
	defaultPairs := []string{"BTCUSDT", "TRXUSDT", "SUIUSDT", "SOLUSDT", "NEARUSDT", "TONUSDT", "ICPUSDT"}
	for _, ticker := range defaultPairs {
		if _, exists := positions[ticker]; !exists {
			positions[ticker] = 0
		}
	}

	return positions, nil
}