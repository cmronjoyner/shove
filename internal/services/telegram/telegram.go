package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"gitlab.com/pennersr/shove/internal/queue"
	"gitlab.com/pennersr/shove/internal/services"
)

// TelegramService ...
type TelegramService struct {
	botToken string
	wg       sync.WaitGroup
	log      *log.Logger
	workers  int
	pump     *services.Pump
}

// NewTelegramService ...
func NewTelegramService(botToken string, log *log.Logger, workers int) (tg *TelegramService, err error) {
	tg = &TelegramService{
		botToken: botToken,
		log:      log,
		workers:  workers,
	}
	tg.pump = services.NewPump(workers, log, tg)
	return
}

// ID ...
func (tg *TelegramService) ID() string {
	return "telegram"

}

// String ...
func (tg *TelegramService) String() string {
	return "Telegram"
}

func (tg *TelegramService) NewClient() (services.PumpClient, error) {
	client := &http.Client{
		Timeout: time.Duration(15 * time.Second),
	}
	return client, nil
}

func (tg *TelegramService) PushMessage(pclient services.PumpClient, smsg services.ServiceMessage, fc services.FeedbackCollector) (status services.PushStatus) {
	client := pclient.(*http.Client)
	msg := smsg.(*telegramMessage)
	startedAt := time.Now()
	var success bool

	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", tg.botToken, msg.Method)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(msg.Payload))
	if err != nil {
		tg.log.Println("[ERROR] Creating request:", err)
		return services.PushStatusHardFail
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		tg.log.Println("[ERROR] Posting:", err)
		return services.PushStatusTempFail
	}
	duration := time.Now().Sub(startedAt)

	defer func() {
		fc.CountPush(tg.ID(), success, duration)
	}()

	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		tg.log.Println("[ERROR] Throttled, too many requests: 429")
		return services.PushStatusTempFail
	}

	var respData struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}

	err = json.NewDecoder(resp.Body).Decode(&respData)
	if err != nil {
		tg.log.Println("[ERROR] Decoding response:", err)
		return services.PushStatusTempFail
	}

	// It's a bit odd that an invalid chat ID results in a 400 instead of a
	// special response code {"ok":false,"error_code":400,"description":"Bad
	// Request: chat not found"}
	if respData.ErrorCode == 400 && strings.Contains(respData.Description, "chat not found") {
		fc.TokenInvalid(tg.ID(), msg.ChatID)
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		tg.log.Printf("[ERROR] Rejected: %s (%d), HTTP status: %d", respData.Description, respData.ErrorCode, resp.StatusCode)
		return services.PushStatusHardFail
	}
	if resp.StatusCode >= 500 && resp.StatusCode < 600 {
		tg.log.Println("[ERROR] Upstream HTTP status:", resp.StatusCode)
		return services.PushStatusTempFail
	}
	tg.log.Println("Pushed, took", duration)
	return services.PushStatusSuccess
}

// Serve ...
func (tg *TelegramService) Serve(ctx context.Context, q queue.Queue, fc services.FeedbackCollector) (err error) {
	return tg.pump.Serve(ctx, q, fc)
}
