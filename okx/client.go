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
		APIKey:     apiKey,
		SecretKey:  secretKey,
		Passphrase: passphrase,
		BaseURL:    "https://www.okx.com",
	}
}

func (c *Client) makeRequest(method, endpoint string, body interface{}, responseHolder interface{}) error {
	url := c.BaseURL + endpoint
	var req *http.Request
	var err error

	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("error marshaling request body: %v", err)
		}
		req, err = http.NewRequest(method, url, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return fmt.Errorf("error creating request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest(method, url, nil)
		if err != nil {
			return fmt.Errorf("error creating request: %v", err)
		}
	}

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.999Z")
	bodyStr := ""
	if body != nil {
		bodyBytes, _ := json.Marshal(body)
		bodyStr = string(bodyBytes)
	}
	message := timestamp + method + endpoint + bodyStr
	log.Printf("Signing message: %s", message)

	signature := c.sign(message)
	req.Header.Set("OK-ACCESS-KEY", c.APIKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.Passphrase)
	log.Printf("Generated signature: %s", signature)

	client := &http.Client{}
	log.Printf("Sending %s request to %s", method, url)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request: %v", err)
	}
	defer resp.Body.Close()

	// Read response body for logging
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %v", err)
	}

	// Log truncated response body
	responseStr := string(bodyBytes)
	if len(responseStr) > 1000 {
		responseStr = responseStr[:1000] + "..."
	}
	log.Printf("OKX API response: %s", responseStr)

	if resp.StatusCode != http.StatusOK {
		log.Printf("OKX API error: status %d, body %s", resp.StatusCode, responseStr)
		return fmt.Errorf("OKX API error: status %d", resp.StatusCode)
	}

	if responseHolder != nil {
		// Decode response into responseHolder using the read body
		if err := json.Unmarshal(bodyBytes, responseHolder); err != nil {
			return fmt.Errorf("error decoding response: %v", err)
		}
	}

	return nil
}

func (c *Client) sign(message string) string {
	key := []byte(c.SecretKey)
	h := hmac.New(sha256.New, key)
	h.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func getPrecision(lotSize float64) int {
	if lotSize == 0 {
		return 0
	}
	lotStr := fmt.Sprintf("%f", lotSize)
	if strings.Contains(lotStr, ".") {
		parts := strings.Split(lotStr, ".")
		return len(parts[1])
	}
	return 0
}

func (c *Client) GetSpotBalance() (float64, error) {
	endpoint := "/api/v5/account/balance?ccy=USDT"
	var balance struct {
		Code string `json:"code"`
		Data []struct {
			Details []struct {
				Ccy      string `json:"ccy"`
				CashBal  string `json:"cashBal"`
				AvailBal string `json:"availBal"`
			} `json:"details"`
		} `json:"data"`
	}
	err := c.makeRequest("GET", endpoint, nil, &balance)
	if err != nil {
		return 0, fmt.Errorf("error fetching balance: %v", err)
	}
	if balance.Code != "0" {
		return 0, fmt.Errorf("OKX API balance error: code=%s", balance.Code)
	}
	for _, detail := range balance.Data[0].Details {
		if detail.Ccy == "USDT" {
			availBal, err := strconv.ParseFloat(detail.AvailBal, 64)
			if err != nil {
				return 0, fmt.Errorf("error parsing USDT availBal: %v", err)
			}
			log.Printf("Available USDT balance: %.2f (Total: %s)", availBal, detail.CashBal)
			return availBal, nil
		}
	}
	return 0, fmt.Errorf("USDT balance not found")
}

func (c *Client) GetOpenOrders(ticker string) (bool, error) {
	instId := strings.Replace(ticker, "USDT", "-USDT", 1)
	endpoint := fmt.Sprintf("/api/v5/trade/orders-pending?instId=%s", instId)
	var response struct {
		Code string `json:"code"`
		Data []struct {
			OrdId string `json:"ordId"`
		} `json:"data"`
	}
	err := c.makeRequest("GET", endpoint, nil, &response)
	if err != nil {
		return false, fmt.Errorf("error fetching open orders: %v", err)
	}
	if response.Code != "0" {
		return false, fmt.Errorf("OKX API open orders error: code=%s", response.Code)
	}
	return len(response.Data) > 0, nil
}

func (c *Client) getCurrentPrice(ticker string) float64 {
	instId := strings.Replace(ticker, "USDT", "-USDT", 1)
	endpoint := fmt.Sprintf("/api/v5/market/ticker?instId=%s", instId)
	var result struct {
		Code string `json:"code"`
		Data []struct {
			Last string `json:"last"`
		} `json:"data"`
	}
	if err := c.makeRequest("GET", endpoint, nil, &result); err != nil {
		log.Printf("Error fetching price for %s: %v", ticker, err)
		return 0
	}
	if result.Code != "0" || len(result.Data) == 0 {
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

func (c *Client) PlaceOrder(ticker, side string, size, lotSize float64) error {
	instId := strings.Replace(ticker, "USDT", "-USDT", 1)
	precision := getPrecision(lotSize)
	formattedSize := fmt.Sprintf("%.*f", precision, size)

	// Limit order
	price := c.getCurrentPrice(ticker)
	if price == 0 {
		return fmt.Errorf("failed to get price for %s", ticker)
	}
	priceAdjust := price
	if side == "buy" {
		priceAdjust *= 1.001 // 0.1% above for buy
	} else {
		priceAdjust *= 0.999 // 0.1% below for sell
	}
	bodyMap := map[string]string{
		"instId":  instId,
		"ordType": "limit",
		"side":    side,
		"sz":      formattedSize,
		"tdMode":  "cash",
		"px":      fmt.Sprintf("%.8f", priceAdjust),
	}

	log.Printf("Formatted order size for %s: %s (lotSize: %f, precision: %d)", ticker, formattedSize, lotSize, precision)
	log.Printf("Sending order request: %v", bodyMap)

	var response struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			OrdId string `json:"ordId"`
			SCode string `json:"sCode"`
			SMsg  string `json:"sMsg"`
		} `json:"data"`
	}
	err := c.makeRequest("POST", "/api/v5/trade/order", bodyMap, &response)
	if err != nil {
		return fmt.Errorf("error placing order: %v", err)
	}
	if response.Code != "0" || len(response.Data) == 0 || response.Data[0].SCode != "0" {
		log.Printf("Order failed for %s: code=%s, sCode=%s, sMsg=%s", ticker, response.Code, response.Data[0].SCode, response.Data[0].SMsg)
		return fmt.Errorf("OKX API order error: code=%s, msg=%s", response.Data[0].SCode, response.Data[0].SMsg)
	}
	log.Printf("Order placed successfully for %s: ordId=%s", ticker, response.Data[0].OrdId)
	return nil
}

func (c *Client) GetPositions() (map[string]float64, error) {
	endpoint := "/api/v5/account/balance"
	var balance struct {
		Code string `json:"code"`
		Data []struct {
			Details []struct {
				Ccy     string `json:"ccy"`
				AvailEq string `json:"availEq"`
				AvailBal string `json:"availBal"`
			} `json:"details"`
		} `json:"data"`
	}
	err := c.makeRequest("GET", endpoint, nil, &balance)
	if err != nil {
		return nil, fmt.Errorf("error fetching positions: %v", err)
	}
	if balance.Code != "0" {
		return nil, fmt.Errorf("OKX API balance error: code=%s", balance.Code)
	}

	positions := make(map[string]float64)
	for _, detail := range balance.Data[0].Details {
		for _, pair := range []string{"BTCUSDT", "TRXUSDT", "SUIUSDT", "SOLUSDT", "NEARUSDT", "TONUSDT", "ICPUSDT"} {
			if strings.HasPrefix(pair, detail.Ccy) {
				// Try availEq first, fall back to availBal if empty or invalid
				availVal := detail.AvailEq
				if availVal == "" {
					availVal = detail.AvailBal
				}
				availEq, err := strconv.ParseFloat(availVal, 64)
				if err != nil {
					log.Printf("Error parsing availEq/availBal for %s: %v", detail.Ccy, err)
					continue
				}
				positions[pair] = availEq
			}
		}
	}
	return positions, nil
}