package main

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
	"os"
	"strings"
	"time"
)

type Alert struct {
	Ticker string `json:"ticker"`
	Signal string `json:"signal"`
}

type OKXClient struct {
	APIKey    string
	SecretKey string
	Passphrase string
}

func NewOKXClient(apiKey, secretKey, passphrase string) *OKXClient {
	return &OKXClient{
		APIKey:    apiKey,
		SecretKey: secretKey,
		Passphrase: passphrase,
	}
}

func (c *OKXClient) signRequest(timestamp, method, requestPath, body string) string {
	message := fmt.Sprintf("%s%s%s%s", timestamp, strings.ToUpper(method), requestPath, body)
	mac := hmac.New(sha256.New, []byte(c.SecretKey))
	mac.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (c *OKXClient) placeOrder(ticker, signal string) error {
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
		"sz":      "0.01", // Example size, adjust as needed
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

	log.Printf("Order placed successfully for %s with signal %s", ticker, signal)
	return nil
}

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

	client := NewOKXClient(
		os.Getenv("OKX_API_KEY"),
		os.Getenv("OKX_SECRET_KEY"),
		os.Getenv("OKX_PASSPHRASE"),
	)

	err := client.placeOrder(alert.Ticker, alert.Signal)
	if err != nil {
		log.Printf("Error placing order: %v", err)
		http.Error(w, "Failed to place order", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Alert processed: %s %s", alert.Ticker, alert.Signal)
}

func main() {
	http.HandleFunc("/webhook", handler)
	port := ":8080"
	log.Printf("Server starting on port %s...", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}