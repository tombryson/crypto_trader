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

type Client struct {
	APIKey    string
	SecretKey string
	Passphrase string
}

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
	instrumentID := fmt.Sprintf("%s-USDT", strings.ToUpper(ticker))

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
	baseURL := "https://www.okx.com"
	endpoint := "/api/v5/account/positions?instType=SPOT"
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

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OKX API error: %s, status: %d", string(bodyBytes), resp.StatusCode)
	}

	var result struct {
		Data []struct {
			InstId   string `json:"instId"`
			Pos      string `json:"pos"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	positions := make(map[string]float64)
	for _, pos := range result.Data {
		if strings.HasSuffix(pos.InstId, "-USDT") {
			size, err := strconv.ParseFloat(pos.Pos, 64)
			if err != nil {
				return nil, err
			}
			positions[pos.InstId] = size
		}
	}
	return positions, nil
}