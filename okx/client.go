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

type OrderResponse struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []struct {
		OrdId   string `json:"ordId"`
		ClOrdId string `json:"clOrdId"`
		SCode   string `json:"sCode"`
		SMsg    string `json:"sMsg"`
	} `json:"data"`
}

type BalanceResponse struct {
	Data []struct {
		Details []struct {
			Ccy       string `json:"ccy"`
			AvailBal  string `json:"availBal"`
			FrozenBal string `json:"frozenBal"`
		} `json:"details"`
	} `json:"data"`
}

type OpenOrdersResponse struct {
	Data []struct {
		InstId string `json:"instId"`
	} `json:"data"`
}

func NewClient(apiKey, secretKey, passphrase string) *Client {
	return &Client{
		APIKey:     apiKey,
		SecretKey:  secretKey,
		Passphrase: passphrase,
		BaseURL:    "https://www.okx.com",
	}
}

func getPrecision(lotSize float64) int {
	if lotSize <= 0 || lotSize < 0.0001 || lotSize > 1 {
		log.Printf("Invalid lotSize %f, using default precision 1", lotSize)
		return 1 // Default to 1 decimal place (e.g., for lotSize 0.1)
	}
	// Calculate precision based on significant decimals
	str := fmt.Sprintf("%.10f", lotSize)
	str = strings.TrimRight(str, "0")
	if idx := strings.Index(str, "."); idx != -1 {
		return len(str) - idx - 1
	}
	return 1
}

func (c *Client) sign(timestamp, method, requestPath, body string) string {
	message := timestamp + method + requestPath + body
	mac := hmac.New(sha256.New, []byte(c.SecretKey))
	mac.Write([]byte(message))
	log.Printf("Signing message: %s", message)
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	log.Printf("Generated signature: %s", signature)
	return signature
}

func (c *Client) PlaceOrder(ticker, signal string, size, lotSize float64) error {
	precision := getPrecision(lotSize)
	formattedSize := fmt.Sprintf("%.*f", precision, size)
	log.Printf("Formatted order size for %s: %s (lotSize: %f, precision: %d)", ticker, formattedSize, lotSize, precision)

	instId := strings.Replace(ticker, "USDT", "-USDT", 1)
	side := signal
	bodyMap := map[string]string{
		"instId":  instId,
		"ordType": "market",
		"side":    side,
		"sz":      formattedSize,
		"tdMode":  "cash",
	}
	bodyBytes, _ := json.Marshal(bodyMap)
	body := string(bodyBytes)
	log.Printf("Sending order request: %s", body)

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	method := "POST"
	requestPath := "/api/v5/trade/order"
	signature := c.sign(timestamp, method, requestPath, body)

	req, err := http.NewRequest(method, c.BaseURL+requestPath, bytes.NewReader(bodyBytes))
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	log.Printf("OKX API response: %s", respBody)

	var orderResp OrderResponse
	if err := json.Unmarshal(respBody, &orderResp); err != nil {
		return fmt.Errorf("error decoding order response: %v", err)
	}

	if orderResp.Code != "0" {
		for _, data := range orderResp.Data {
			if data.SCode != "" && data.SCode != "0" {
				return fmt.Errorf("OKX API order error: code=%s, msg=%s", data.SCode, data.SMsg)
			}
		}
		return fmt.Errorf("OKX API order error: code=%s, msg=%s", orderResp.Code, orderResp.Msg)
	}

	return nil
}

func (c *Client) GetSpotBalance() (float64, error) {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	method := "GET"
	requestPath := "/api/v5/account/balance?ccy=USDT"
	body := ""
	signature := c.sign(timestamp, method, requestPath, body)

	req, err := http.NewRequest(method, c.BaseURL+requestPath, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("OK-ACCESS-KEY", c.APIKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.Passphrase)
	log.Printf("Sending balances request to %s", c.BaseURL+requestPath)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var balanceResp BalanceResponse
	if err := json.Unmarshal(bodyBytes, &balanceResp); err != nil {
		return 0, fmt.Errorf("error decoding balance response: %v", err)
	}

	for _, data := range balanceResp.Data {
		for _, detail := range data.Details {
			if detail.Ccy == "USDT" {
				availBal, err := strconv.ParseFloat(detail.AvailBal, 64)
				if err != nil {
					return 0, fmt.Errorf("error parsing available balance: %v", err)
				}
				log.Printf("Available USDT balance: %.2f (Total: %s)", availBal, detail.AvailBal)
				return availBal, nil
			}
		}
	}

	return 0, fmt.Errorf("USDT balance not found")
}

func (c *Client) GetOpenOrders(ticker string) (bool, error) {
	instId := strings.Replace(ticker, "USDT", "-USDT", 1)
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	method := "GET"
	requestPath := "/api/v5/trade/orders-pending?instId=" + instId
	body := ""
	signature := c.sign(timestamp, method, requestPath, body)

	req, err := http.NewRequest(method, c.BaseURL+requestPath, nil)
	if err != nil {
		return false, err
	}

	req.Header.Set("OK-ACCESS-KEY", c.APIKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.Passphrase)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	var openOrders OpenOrdersResponse
	if err := json.Unmarshal(bodyBytes, &openOrders); err != nil {
		return false, fmt.Errorf("error decoding open orders response: %v", err)
	}

	return len(openOrders.Data) > 0, nil
}

func (c *Client) GetPositions() (map[string]float64, error) {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	method := "GET"
	requestPath := "/api/v5/account/balance"
	body := ""
	signature := c.sign(timestamp, method, requestPath, body)

	req, err := http.NewRequest(method, c.BaseURL+requestPath, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("OK-ACCESS-KEY", c.APIKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.Passphrase)
	log.Printf("Sending balances request to %s", c.BaseURL+requestPath)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var balanceResp BalanceResponse
	if err := json.Unmarshal(bodyBytes, &balanceResp); err != nil {
		return nil, fmt.Errorf("error decoding balance response: %v", err)
	}

	positions := make(map[string]float64)
	for _, data := range balanceResp.Data {
		for _, detail := range data.Details {
			if strings.HasSuffix(detail.Ccy, "USDT") {
				continue
			}
			availBal, err := strconv.ParseFloat(detail.AvailBal, 64)
			if err != nil {
				log.Printf("Error parsing available balance for %s: %v", detail.Ccy, err)
				continue
			}
			ticker := detail.Ccy + "USDT"
			positions[ticker] = availBal
		}
	}

	return positions, nil
}