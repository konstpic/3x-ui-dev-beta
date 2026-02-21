package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v2/config"
	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/common"
	"github.com/mhsanaei/3x-ui/v2/web/global"
	"github.com/mhsanaei/3x-ui/v2/web/locale"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"gorm.io/gorm"

	"github.com/google/uuid"
	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/skip2/go-qrcode"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpproxy"
)

var (
	bot *telego.Bot

	// botCancel stores the function to cancel the context, stopping Long Polling gracefully.
	botCancel context.CancelFunc
	// tgBotMutex protects concurrent access to botCancel variable
	tgBotMutex sync.Mutex
	// botWG waits for the OnReceive Long Polling goroutine to finish.
	botWG sync.WaitGroup

	botHandler  *th.BotHandler
	adminIds    []int64
	isRunning   bool
	hostname    string
	hashStorage *global.HashStorage

	// Performance improvements
	messageWorkerPool   chan struct{} // Semaphore for limiting concurrent message processing
	optimizedHTTPClient *http.Client  // HTTP client with connection pooling and timeouts

	// Simple cache for frequently accessed data
	statusCache struct {
		data      *Status
		timestamp time.Time
		mutex     sync.RWMutex
	}

	serverStatsCache struct {
		data      string
		timestamp time.Time
		mutex     sync.RWMutex
	}

	// clients data to adding new client
	receiver_inbound_ID int
	client_Id           string
	client_Flow         string
	client_Email        string
	client_TotalGB      int64
	client_ExpiryTime   int64
	client_Enable       bool
	client_TgID         string
	client_SubID        string
	client_Comment      string
	client_Reset        int
	client_Security     string
	client_ShPassword   string
	client_TrPassword   string
	client_Method       string
	client_MaxHWID      int
	client_HWIDEnabled  bool
)

var userStates = make(map[int64]string)

// LoginStatus represents the result of a login attempt.
type LoginStatus byte

// Login status constants
const (
	LoginSuccess        LoginStatus = 1        // Login was successful
	LoginFail           LoginStatus = 0        // Login failed
	EmptyTelegramUserID             = int64(0) // Default value for empty Telegram user ID
)

// Tgbot provides business logic for Telegram bot integration.
// It handles bot commands, user interactions, and status reporting via Telegram.
type Tgbot struct {
	inboundService InboundService
	settingService SettingService
	serverService  ServerService
	xrayService    XrayService
	lastStatus     *Status
}

// NewTgbot creates a new Tgbot instance.
func (t *Tgbot) NewTgbot() *Tgbot {
	return new(Tgbot)
}

// I18nBot retrieves a localized message for the bot interface.
func (t *Tgbot) I18nBot(name string, params ...string) string {
	return locale.I18n(locale.Bot, name, params...)
}

// GetHashStorage returns the hash storage instance for callback queries.
func (t *Tgbot) GetHashStorage() *global.HashStorage {
	return hashStorage
}

// getCachedStatus returns cached server status if it's fresh enough (less than 5 seconds old)
func (t *Tgbot) getCachedStatus() (*Status, bool) {
	statusCache.mutex.RLock()
	defer statusCache.mutex.RUnlock()

	if statusCache.data != nil && time.Since(statusCache.timestamp) < 5*time.Second {
		return statusCache.data, true
	}
	return nil, false
}

// setCachedStatus updates the status cache
func (t *Tgbot) setCachedStatus(status *Status) {
	statusCache.mutex.Lock()
	defer statusCache.mutex.Unlock()

	statusCache.data = status
	statusCache.timestamp = time.Now()
}

// getCachedServerStats returns cached server stats if it's fresh enough (less than 10 seconds old)
func (t *Tgbot) getCachedServerStats() (string, bool) {
	serverStatsCache.mutex.RLock()
	defer serverStatsCache.mutex.RUnlock()

	if serverStatsCache.data != "" && time.Since(serverStatsCache.timestamp) < 10*time.Second {
		return serverStatsCache.data, true
	}
	return "", false
}

// setCachedServerStats updates the server stats cache
func (t *Tgbot) setCachedServerStats(stats string) {
	serverStatsCache.mutex.Lock()
	defer serverStatsCache.mutex.Unlock()

	serverStatsCache.data = stats
	serverStatsCache.timestamp = time.Now()
}

// Start initializes and starts the Telegram bot with the provided translation files.
func (t *Tgbot) Start(i18nFS embed.FS) error {
	// Initialize localizer
	err := locale.InitLocalizer(i18nFS, &t.settingService)
	if err != nil {
		return err
	}

	// If Start is called again (e.g. during reload), ensure any previous long-polling
	// loop is stopped before creating a new bot / receiver.
	StopBot()

	// Initialize hash storage to store callback queries
	hashStorage = global.NewHashStorage(20 * time.Minute)

	// Initialize worker pool for concurrent message processing (max 10 concurrent handlers)
	messageWorkerPool = make(chan struct{}, 10)

	// Initialize optimized HTTP client with connection pooling
	optimizedHTTPClient = &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     30 * time.Second,
			DisableKeepAlives:   false,
		},
	}

	t.SetHostname()

	// Get Telegram bot token
	tgBotToken, err := t.settingService.GetTgBotToken()
	if err != nil || tgBotToken == "" {
		logger.Warning("Failed to get Telegram bot token:", err)
		return err
	}

	// Get Telegram bot chat ID(s)
	tgBotID, err := t.settingService.GetTgBotChatId()
	if err != nil {
		logger.Warning("Failed to get Telegram bot chat ID:", err)
		return err
	}

	parsedAdminIds := make([]int64, 0)
	// Parse admin IDs from comma-separated string
	if tgBotID != "" {
		for _, adminID := range strings.Split(tgBotID, ",") {
			id, err := strconv.ParseInt(adminID, 10, 64)
			if err != nil {
				logger.Warning("Failed to parse admin ID from Telegram bot chat ID:", err)
				return err
			}
			parsedAdminIds = append(parsedAdminIds, int64(id))
		}
	}
	tgBotMutex.Lock()
	adminIds = parsedAdminIds
	tgBotMutex.Unlock()

	// Get Telegram bot proxy URL
	tgBotProxy, err := t.settingService.GetTgBotProxy()
	if err != nil {
		logger.Warning("Failed to get Telegram bot proxy URL:", err)
	}

	// Get Telegram bot API server URL
	tgBotAPIServer, err := t.settingService.GetTgBotAPIServer()
	if err != nil {
		logger.Warning("Failed to get Telegram bot API server URL:", err)
	}

	// Create new Telegram bot instance
	bot, err = t.NewBot(tgBotToken, tgBotProxy, tgBotAPIServer)
	if err != nil {
		logger.Error("Failed to initialize Telegram bot API:", err)
		return err
	}

	// After bot initialization, set up bot commands with localized descriptions
	err = bot.SetMyCommands(context.Background(), &telego.SetMyCommandsParams{
		Commands: []telego.BotCommand{
			{Command: "start", Description: t.I18nBot("tgbot.commands.startDesc")},
			{Command: "help", Description: t.I18nBot("tgbot.commands.helpDesc")},
			{Command: "status", Description: t.I18nBot("tgbot.commands.statusDesc")},
			{Command: "id", Description: t.I18nBot("tgbot.commands.idDesc")},
		},
	})
	if err != nil {
		logger.Warning("Failed to set bot commands:", err)
	}

	// Start receiving Telegram bot messages
	tgBotMutex.Lock()
	alreadyRunning := isRunning || botCancel != nil
	tgBotMutex.Unlock()
	if !alreadyRunning {
		logger.Info("Telegram bot receiver started")
		go t.OnReceive()
	}

	return nil
}

// NewBot creates a new Telegram bot instance with optional proxy and API server settings.
func (t *Tgbot) NewBot(token string, proxyUrl string, apiServerUrl string) (*telego.Bot, error) {
	if proxyUrl == "" && apiServerUrl == "" {
		return telego.NewBot(token)
	}

	if proxyUrl != "" {
		if !strings.HasPrefix(proxyUrl, "socks5://") {
			logger.Warning("Invalid socks5 URL, using default")
			return telego.NewBot(token)
		}

		_, err := url.Parse(proxyUrl)
		if err != nil {
			logger.Warningf("Can't parse proxy URL, using default instance for tgbot: %v", err)
			return telego.NewBot(token)
		}

		return telego.NewBot(token, telego.WithFastHTTPClient(&fasthttp.Client{
			Dial: fasthttpproxy.FasthttpSocksDialer(proxyUrl),
		}))
	}

	if !strings.HasPrefix(apiServerUrl, "http") {
		logger.Warning("Invalid http(s) URL, using default")
		return telego.NewBot(token)
	}

	_, err := url.Parse(apiServerUrl)
	if err != nil {
		logger.Warningf("Can't parse API server URL, using default instance for tgbot: %v", err)
		return telego.NewBot(token)
	}

	return telego.NewBot(token, telego.WithAPIServer(apiServerUrl))
}

// IsRunning checks if the Telegram bot is currently running.
func (t *Tgbot) IsRunning() bool {
	tgBotMutex.Lock()
	defer tgBotMutex.Unlock()
	return isRunning
}

// SetHostname sets the hostname for the bot.
func (t *Tgbot) SetHostname() {
	host, err := os.Hostname()
	if err != nil {
		logger.Error("get hostname error:", err)
		hostname = ""
		return
	}
	hostname = host
}

// Stop safely stops the Telegram bot's Long Polling operation.
// This method now calls the global StopBot function and cleans up other resources.
func (t *Tgbot) Stop() {
	StopBot()
	logger.Info("Stop Telegram receiver ...")
	tgBotMutex.Lock()
	adminIds = nil
	tgBotMutex.Unlock()
}

// StopBot safely stops the Telegram bot's Long Polling operation by cancelling its context.
// This is the global function called from main.go's signal handler and t.Stop().
func StopBot() {
	// Don't hold the mutex while cancelling/waiting.
	tgBotMutex.Lock()
	cancel := botCancel
	botCancel = nil
	handler := botHandler
	botHandler = nil
	isRunning = false
	tgBotMutex.Unlock()

	if handler != nil {
		handler.Stop()
	}

	if cancel != nil {
		logger.Info("Sending cancellation signal to Telegram bot...")
		// Cancels the context passed to UpdatesViaLongPolling; this closes updates channel
		// and lets botHandler.Start() exit cleanly.
		cancel()
		botWG.Wait()
		logger.Info("Telegram bot successfully stopped.")
	}
}

// encodeQuery encodes the query string if it's longer than 64 characters.
func (t *Tgbot) encodeQuery(query string) string {
	// NOTE: we only need to hash for more than 64 chars
	if len(query) <= 64 {
		return query
	}

	return hashStorage.SaveHash(query)
}

// decodeQuery decodes a hashed query string back to its original form.
func (t *Tgbot) decodeQuery(query string) (string, error) {
	if !hashStorage.IsMD5(query) {
		return query, nil
	}

	decoded, exists := hashStorage.GetValue(query)
	if !exists {
		return "", common.NewError("hash not found in storage!")
	}

	return decoded, nil
}

// OnReceive starts the message receiving loop for the Telegram bot.
func (t *Tgbot) OnReceive() {
	params := telego.GetUpdatesParams{
		Timeout: 30, // Increased timeout to reduce API calls
	}
	// Strict singleton: never start a second long-polling loop.
	tgBotMutex.Lock()
	if botCancel != nil || isRunning {
		tgBotMutex.Unlock()
		logger.Warning("TgBot OnReceive called while already running; ignoring.")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	botCancel = cancel
	isRunning = true
	// Add to WaitGroup before releasing the lock so StopBot() can't return
	// before this receiver goroutine is accounted for.
	botWG.Add(1)
	tgBotMutex.Unlock()

	// Get updates channel using the context.
	updates, _ := bot.UpdatesViaLongPolling(ctx, &params)
	go func() {
		defer botWG.Done()
		h, _ := th.NewBotHandler(bot, updates)
		tgBotMutex.Lock()
		botHandler = h
		tgBotMutex.Unlock()

		h.HandleMessage(func(ctx *th.Context, message telego.Message) error {
			delete(userStates, message.Chat.ID)
			t.SendMsgToTgbot(message.Chat.ID, t.I18nBot("tgbot.keyboardClosed"), tu.ReplyKeyboardRemove())
			return nil
		}, th.TextEqual(t.I18nBot("tgbot.buttons.closeKeyboard")))

		h.HandleMessage(func(ctx *th.Context, message telego.Message) error {
			// Use goroutine with worker pool for concurrent command processing
			go func() {
				messageWorkerPool <- struct{}{}        // Acquire worker
				defer func() { <-messageWorkerPool }() // Release worker

				delete(userStates, message.Chat.ID)
				t.answerCommand(&message, message.Chat.ID, checkAdmin(message.From.ID))
			}()
			return nil
		}, th.AnyCommand())

		h.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
			// Use goroutine with worker pool for concurrent callback processing
			go func() {
				messageWorkerPool <- struct{}{}        // Acquire worker
				defer func() { <-messageWorkerPool }() // Release worker

				delete(userStates, query.Message.GetChat().ID)
				t.answerCallback(&query, checkAdmin(query.From.ID))
			}()
			return nil
		}, th.AnyCallbackQueryWithMessage())

		h.HandleMessage(func(ctx *th.Context, message telego.Message) error {
			if userState, exists := userStates[message.Chat.ID]; exists {
				switch userState {
				case "awaiting_id":
					if client_Id == strings.TrimSpace(message.Text) {
						t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.using_default_value"), 3, tu.ReplyKeyboardRemove())
						delete(userStates, message.Chat.ID)
						inbound, _ := t.inboundService.GetInbound(receiver_inbound_ID)
						message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
						t.addClient(message.Chat.ID, message_text)
						return nil
					}

					client_Id = strings.TrimSpace(message.Text)
					if t.isSingleWord(client_Id) {
						userStates[message.Chat.ID] = "awaiting_id"

						cancel_btn_markup := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
							),
						)

						t.SendMsgToTgbot(message.Chat.ID, t.I18nBot("tgbot.messages.incorrect_input"), cancel_btn_markup)
					} else {
						t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.received_id"), 3, tu.ReplyKeyboardRemove())
						delete(userStates, message.Chat.ID)
						inbound, _ := t.inboundService.GetInbound(receiver_inbound_ID)
						message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
						t.addClient(message.Chat.ID, message_text)
					}
				case "awaiting_password_tr":
					if client_TrPassword == strings.TrimSpace(message.Text) {
						t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.using_default_value"), 3, tu.ReplyKeyboardRemove())
						delete(userStates, message.Chat.ID)
						return nil
					}

					client_TrPassword = strings.TrimSpace(message.Text)
					if t.isSingleWord(client_TrPassword) {
						userStates[message.Chat.ID] = "awaiting_password_tr"

						cancel_btn_markup := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
							),
						)

						t.SendMsgToTgbot(message.Chat.ID, t.I18nBot("tgbot.messages.incorrect_input"), cancel_btn_markup)
					} else {
						t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.received_password"), 3, tu.ReplyKeyboardRemove())
						delete(userStates, message.Chat.ID)
						inbound, _ := t.inboundService.GetInbound(receiver_inbound_ID)
						message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
						t.addClient(message.Chat.ID, message_text)
					}
				case "awaiting_password_sh":
					if client_ShPassword == strings.TrimSpace(message.Text) {
						t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.using_default_value"), 3, tu.ReplyKeyboardRemove())
						delete(userStates, message.Chat.ID)
						return nil
					}

					client_ShPassword = strings.TrimSpace(message.Text)
					if t.isSingleWord(client_ShPassword) {
						userStates[message.Chat.ID] = "awaiting_password_sh"

						cancel_btn_markup := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
							),
						)

						t.SendMsgToTgbot(message.Chat.ID, t.I18nBot("tgbot.messages.incorrect_input"), cancel_btn_markup)
					} else {
						t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.received_password"), 3, tu.ReplyKeyboardRemove())
						delete(userStates, message.Chat.ID)
						inbound, _ := t.inboundService.GetInbound(receiver_inbound_ID)
						message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
						t.addClient(message.Chat.ID, message_text)
					}
				case "awaiting_email":
					newEmail := strings.ToLower(strings.TrimSpace(message.Text))
					if strings.ToLower(client_Email) == newEmail {
						t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.using_default_value"), 3, tu.ReplyKeyboardRemove())
						delete(userStates, message.Chat.ID)
						return nil
					}

					client_Email = newEmail // Already normalized to lowercase
					logger.Debugf("awaiting_email: Updated email to '%s'", client_Email)
					if t.isSingleWord(client_Email) {
						userStates[message.Chat.ID] = "awaiting_email"

						cancel_btn_markup := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
							),
						)

						t.SendMsgToTgbot(message.Chat.ID, t.I18nBot("tgbot.messages.incorrect_input"), cancel_btn_markup)
					} else {
						t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.received_email"), 3, tu.ReplyKeyboardRemove())
						delete(userStates, message.Chat.ID)
						inbound, _ := t.inboundService.GetInbound(receiver_inbound_ID)
						message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
						t.addClient(message.Chat.ID, message_text)
					}
				case "awaiting_comment":
					if client_Comment == strings.TrimSpace(message.Text) {
						t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.using_default_value"), 3, tu.ReplyKeyboardRemove())
						delete(userStates, message.Chat.ID)
						return nil
					}

					client_Comment = strings.TrimSpace(message.Text)
					t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.received_comment"), 3, tu.ReplyKeyboardRemove())
					delete(userStates, message.Chat.ID)
					inbound, _ := t.inboundService.GetInbound(receiver_inbound_ID)
					message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
					t.addClient(message.Chat.ID, message_text)
				}

			} else {
				if message.UsersShared != nil {
					if checkAdmin(message.From.ID) {
						for _, sharedUser := range message.UsersShared.Users {
							userID := sharedUser.UserID
							// RequestID is now clientEntity.Id (new architecture) instead of traffic.Id
							clientId := int(message.UsersShared.RequestID)
							
							// Use ClientService to update TgID directly (new architecture)
							clientService := ClientService{}
							client, err := clientService.GetClient(clientId)
							if err != nil {
								output := t.I18nBot("tgbot.messages.selectUserFailed")
								t.SendMsgToTgbot(message.Chat.ID, output, tu.ReplyKeyboardRemove())
								continue
							}
							
							// Update TgID
							client.TgID = userID
							client.UpdatedAt = time.Now().Unix()
							needRestart, err := clientService.UpdateClient(client.UserId, client)
							if needRestart {
								t.xrayService.SetToNeedRestart()
							}
							
							output := ""
							if err != nil {
								output += t.I18nBot("tgbot.messages.selectUserFailed")
							} else {
								output += t.I18nBot("tgbot.messages.userSaved")
							}
							t.SendMsgToTgbot(message.Chat.ID, output, tu.ReplyKeyboardRemove())
						}
					} else {
						t.SendMsgToTgbot(message.Chat.ID, t.I18nBot("tgbot.noResult"), tu.ReplyKeyboardRemove())
					}
				}
			}
			return nil
		}, th.AnyMessage())

		h.Start()
	}()
}

// answerCommand processes incoming command messages from Telegram users.
func (t *Tgbot) answerCommand(message *telego.Message, chatId int64, isAdmin bool) {
	msg, onlyMessage := "", false

	command, _, commandArgs := tu.ParseCommand(message.Text)

	// Helper function to handle unknown commands.
	handleUnknownCommand := func() {
		msg += t.I18nBot("tgbot.commands.unknown")
	}

	// Handle the command.
	switch command {
	case "help":
		msg += t.I18nBot("tgbot.commands.help")
		msg += t.I18nBot("tgbot.commands.pleaseChoose")
	case "start":
		msg += t.I18nBot("tgbot.commands.start", "Firstname=="+message.From.FirstName)
		if isAdmin {
			msg += t.I18nBot("tgbot.commands.welcome", "Hostname=="+hostname)
		}
		msg += "\n\n" + t.I18nBot("tgbot.commands.pleaseChoose")
	case "status":
		onlyMessage = true
		msg += t.I18nBot("tgbot.commands.status")
	case "id":
		onlyMessage = true
		msg += t.I18nBot("tgbot.commands.getID", "ID=="+strconv.FormatInt(message.From.ID, 10))
	case "usage":
		onlyMessage = true
		if len(commandArgs) > 0 {
			if isAdmin {
				t.searchClient(chatId, commandArgs[0])
			} else {
				t.getClientUsage(chatId, int64(message.From.ID), commandArgs[0])
			}
		} else {
			msg += t.I18nBot("tgbot.commands.usage")
		}
	case "inbound":
		onlyMessage = true
		if isAdmin && len(commandArgs) > 0 {
			t.searchInbound(chatId, commandArgs[0])
		} else {
			handleUnknownCommand()
		}
	case "restart":
		onlyMessage = true
		if isAdmin {
			if len(commandArgs) == 0 {
				if t.xrayService.IsXrayRunning() {
					err := t.xrayService.RestartXray(true)
					if err != nil {
						msg += t.I18nBot("tgbot.commands.restartFailed", "Error=="+err.Error())
					} else {
						msg += t.I18nBot("tgbot.commands.restartSuccess")
					}
				} else {
					msg += t.I18nBot("tgbot.commands.xrayNotRunning")
				}
			} else {
				handleUnknownCommand()
				msg += t.I18nBot("tgbot.commands.restartUsage")
			}
		} else {
			handleUnknownCommand()
		}
	default:
		handleUnknownCommand()
	}

	if msg != "" {
		t.sendResponse(chatId, msg, onlyMessage, isAdmin)
	}
}

// sendResponse sends the response message based on the onlyMessage flag.
func (t *Tgbot) sendResponse(chatId int64, msg string, onlyMessage, isAdmin bool) {
	if onlyMessage {
		t.SendMsgToTgbot(chatId, msg)
	} else {
		t.SendAnswer(chatId, msg, isAdmin)
	}
}

// randomLowerAndNum generates a random string of lowercase letters and numbers.
func (t *Tgbot) randomLowerAndNum(length int) string {
	charset := "abcdefghijklmnopqrstuvwxyz0123456789"
	bytes := make([]byte, length)
	for i := range bytes {
		randomIndex, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		bytes[i] = charset[randomIndex.Int64()]
	}
	return string(bytes)
}

// randomShadowSocksPassword generates a random password for Shadowsocks.
func (t *Tgbot) randomShadowSocksPassword() string {
	array := make([]byte, 32)
	_, err := rand.Read(array)
	if err != nil {
		return t.randomLowerAndNum(32)
	}
	return base64.StdEncoding.EncodeToString(array)
}

// answerCallback processes callback queries from inline keyboards.
func (t *Tgbot) answerCallback(callbackQuery *telego.CallbackQuery, isAdmin bool) {
	chatId := callbackQuery.Message.GetChat().ID

	if isAdmin {
		// get query from hash storage
		decodedQuery, err := t.decodeQuery(callbackQuery.Data)
		if err != nil {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.noQuery"))
			return
		}
		dataArray := strings.Split(decodedQuery, " ")

		if len(dataArray) >= 2 && len(dataArray[1]) > 0 {
			email := dataArray[1]
			switch dataArray[0] {
			case "get_client_info":
				// Show client info - this is called from client list
				t.sendCallbackAnswerTgBot(callbackQuery.ID, "")
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
				return
			case "get_clients_for_sub":
				inboundId := dataArray[1]
				inboundIdInt, err := strconv.Atoi(inboundId)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				inbound, err := t.inboundService.GetInbound(inboundIdInt)
				if err != nil || inbound == nil {
					logger.Warningf("Error getting inbound %d: %v", inboundIdInt, err)
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.wentWrong"))
					return
				}
				clientsKB, err := t.getInboundClientsFor(inboundIdInt, "client_sub_links")
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				// Edit message instead of sending new one, fallback to send if edit fails
				err = t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), t.I18nBot("tgbot.answers.chooseClient", "Inbound=="+inbound.Remark), clientsKB)
				if err != nil {
					t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.chooseClient", "Inbound=="+inbound.Remark), clientsKB)
				}
			case "get_clients_for_individual":
				inboundId := dataArray[1]
				inboundIdInt, err := strconv.Atoi(inboundId)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				inbound, err := t.inboundService.GetInbound(inboundIdInt)
				if err != nil || inbound == nil {
					logger.Warningf("Error getting inbound %d: %v", inboundIdInt, err)
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.wentWrong"))
					return
				}
				clientsKB, err := t.getInboundClientsFor(inboundIdInt, "client_individual_links")
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				// Edit message instead of sending new one, fallback to send if edit fails
				err = t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), t.I18nBot("tgbot.answers.chooseClient", "Inbound=="+inbound.Remark), clientsKB)
				if err != nil {
					t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.chooseClient", "Inbound=="+inbound.Remark), clientsKB)
				}
			case "get_clients_for_qr":
				inboundId := dataArray[1]
				inboundIdInt, err := strconv.Atoi(inboundId)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				inbound, err := t.inboundService.GetInbound(inboundIdInt)
				if err != nil || inbound == nil {
					logger.Warningf("Error getting inbound %d: %v", inboundIdInt, err)
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.wentWrong"))
					return
				}
				clientsKB, err := t.getInboundClientsFor(inboundIdInt, "client_qr_links")
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				// Edit message instead of sending new one, fallback to send if edit fails
				err = t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), t.I18nBot("tgbot.answers.chooseClient", "Inbound=="+inbound.Remark), clientsKB)
				if err != nil {
					t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.chooseClient", "Inbound=="+inbound.Remark), clientsKB)
				}
			case "client_sub_links":
				t.sendClientSubLinks(chatId, email, callbackQuery.Message.GetMessageID())
				return
			case "client_individual_links":
				t.sendClientIndividualLinks(chatId, email, callbackQuery.Message.GetMessageID())
				return
			case "client_qr_links":
				t.sendClientQRLinks(chatId, email, callbackQuery.Message.GetMessageID())
				return
			case "delete_message":
				// Extract message ID from callback data
				if len(dataArray) >= 2 {
					msgID, err := strconv.Atoi(dataArray[1])
					if err == nil {
						t.deleteMessageTgBot(chatId, msgID)
						t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
						return
					}
				}
				return
			case "client_get_usage":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.messages.email", "Email=="+email))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "client_refresh":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.clientRefreshSuccess", "Email=="+email))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "client_cancel":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.canceled", "Email=="+email))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "devices_refresh":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.deviceRefreshSuccess", "Email=="+email))
				t.searchClientDevices(chatId, email, callbackQuery.Message.GetMessageID())
			case "devices_cancel":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.canceled", "Email=="+email))
				t.searchClientDevices(chatId, email, callbackQuery.Message.GetMessageID())
			case "tgid_refresh":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.TGIdRefreshSuccess", "Email=="+email))
				t.clientTelegramUserInfo(chatId, email, callbackQuery.Message.GetMessageID())
			case "tgid_cancel":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.canceled", "Email=="+email))
				t.clientTelegramUserInfo(chatId, email, callbackQuery.Message.GetMessageID())
			case "reset_traffic":
				inlineKeyboard := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancelReset")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmResetTraffic")).WithCallbackData(t.encodeQuery("reset_traffic_c "+email)),
					),
				)
				t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
			case "reset_traffic_c":
				// Use ClientService to reset traffic (new architecture)
				clientEntity, userId, err := t.getClientByEmailWithUserId(email)
				if err != nil {
					if err == gorm.ErrRecordNotFound {
						t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
						return
					}
					logger.Warning(err)
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
					return
				}
				
				// Reset traffic counters
				clientEntity.Up = 0
				clientEntity.Down = 0
				clientEntity.AllTime = 0
				clientEntity.UpdatedAt = time.Now().Unix()
				
				clientService := ClientService{}
				needRestart, err := clientService.UpdateClient(userId, clientEntity)
				if needRestart {
					t.xrayService.SetToNeedRestart()
				}
				if err == nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.resetTrafficSuccess", "Email=="+email))
					t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
				} else {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				}
			case "limit_traffic":
				inlineKeyboard := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.unlimited")).WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 0")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.custom")).WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" 0")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton("1 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 1")),
						tu.InlineKeyboardButton("5 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 5")),
						tu.InlineKeyboardButton("10 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 10")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton("20 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 20")),
						tu.InlineKeyboardButton("30 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 30")),
						tu.InlineKeyboardButton("40 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 40")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton("50 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 50")),
						tu.InlineKeyboardButton("60 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 60")),
						tu.InlineKeyboardButton("80 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 80")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton("100 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 100")),
						tu.InlineKeyboardButton("150 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 150")),
						tu.InlineKeyboardButton("200 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 200")),
					),
				)
				t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
			case "limit_traffic_c":
				if len(dataArray) == 3 {
					limitTraffic, err := strconv.Atoi(dataArray[2])
					if err == nil {
						// Use ClientService to set traffic limit (new architecture)
						clientEntity, userId, err := t.getClientByEmailWithUserId(email)
						if err != nil {
							if err == gorm.ErrRecordNotFound {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
								return
							}
							logger.Warning(err)
							t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
							return
						}
						
						// Set traffic limit (convert GB to float64)
						clientEntity.TotalGB = float64(limitTraffic)
						clientEntity.UpdatedAt = time.Now().Unix()
						
						clientService := ClientService{}
						needRestart, err := clientService.UpdateClient(userId, clientEntity)
						if needRestart {
							t.xrayService.SetToNeedRestart()
						}
						if err == nil {
							t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.setTrafficLimitSuccess", "Email=="+email))
							t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
							return
						}
					}
				}
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "limit_traffic_in":
				if len(dataArray) >= 3 {
					oldInputNumber, err := strconv.Atoi(dataArray[2])
					inputNumber := oldInputNumber
					if err == nil {
						if len(dataArray) == 4 {
							num, err := strconv.Atoi(dataArray[3])
							if err == nil {
								switch num {
								case -2:
									inputNumber = 0
								case -1:
									if inputNumber > 0 {
										inputNumber = (inputNumber / 10)
									}
								default:
									inputNumber = (inputNumber * 10) + num
								}
							}
							if inputNumber == oldInputNumber {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
								return
							}
							if inputNumber >= 999999 {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
								return
							}
						}
						inlineKeyboard := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmNumberAdd", "Num=="+strconv.Itoa(inputNumber))).WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" "+strconv.Itoa(inputNumber))),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 1")),
								tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 2")),
								tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 3")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("4").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 4")),
								tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 5")),
								tu.InlineKeyboardButton("6").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 6")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("7").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 7")),
								tu.InlineKeyboardButton("8").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 8")),
								tu.InlineKeyboardButton("9").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 9")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("🔄").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" -2")),
								tu.InlineKeyboardButton("0").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 0")),
								tu.InlineKeyboardButton("⬅️").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" -1")),
							),
						)
						t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
						return
					}
				}
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "add_client_limit_traffic_c":
				limitTraffic, _ := strconv.ParseInt(dataArray[1], 10, 64)
				client_TotalGB = limitTraffic * 1024 * 1024 * 1024
				messageId := callbackQuery.Message.GetMessageID()
				inbound, err := t.inboundService.GetInbound(receiver_inbound_ID)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}

				t.addClient(callbackQuery.Message.GetChat().ID, message_text, messageId)
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
			case "add_client_limit_traffic_in":
				if len(dataArray) >= 2 {
					oldInputNumber, err := strconv.Atoi(dataArray[1])
					inputNumber := oldInputNumber
					if err == nil {
						if len(dataArray) == 3 {
							num, err := strconv.Atoi(dataArray[2])
							if err == nil {
								switch num {
								case -2:
									inputNumber = 0
								case -1:
									if inputNumber > 0 {
										inputNumber = (inputNumber / 10)
									}
								default:
									inputNumber = (inputNumber * 10) + num
								}
							}
							if inputNumber == oldInputNumber {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
								return
							}
							if inputNumber >= 999999 {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
								return
							}
						}
						inlineKeyboard := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("add_client_default_traffic_exp")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmNumberAdd", "Num=="+strconv.Itoa(inputNumber))).WithCallbackData(t.encodeQuery("add_client_limit_traffic_c "+strconv.Itoa(inputNumber))),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 1")),
								tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 2")),
								tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 3")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("4").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 4")),
								tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 5")),
								tu.InlineKeyboardButton("6").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 6")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("7").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 7")),
								tu.InlineKeyboardButton("8").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 8")),
								tu.InlineKeyboardButton("9").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 9")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("🔄").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" -2")),
								tu.InlineKeyboardButton("0").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 0")),
								tu.InlineKeyboardButton("⬅️").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" -1")),
							),
						)
						t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
						return
					}
				}
			case "reset_exp":
				inlineKeyboard := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancelReset")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.unlimited")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 0")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.custom")).WithCallbackData(t.encodeQuery("reset_exp_in "+email+" 0")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 7 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 7")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 10 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 10")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 14 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 14")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 20 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 20")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 1 "+t.I18nBot("tgbot.month")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 30")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 3 "+t.I18nBot("tgbot.months")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 90")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 6 "+t.I18nBot("tgbot.months")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 180")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 12 "+t.I18nBot("tgbot.months")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 365")),
					),
				)
				t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
			case "reset_exp_c":
				if len(dataArray) == 3 {
					days, err := strconv.ParseInt(dataArray[2], 10, 64)
					if err == nil {
						// Use ClientService to get and update expiry time (new architecture)
						clientEntity, userId, err := t.getClientByEmailWithUserId(email)
						if err != nil {
							if err == gorm.ErrRecordNotFound {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
								return
							}
							logger.Warning(err)
							t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
							return
						}

						var date int64
						if days > 0 {
							if clientEntity.ExpiryTime > 0 {
								if clientEntity.ExpiryTime-time.Now().Unix()*1000 < 0 {
									date = -int64(days * 24 * 60 * 60000)
								} else {
									date = clientEntity.ExpiryTime + int64(days*24*60*60000)
								}
							} else {
								date = clientEntity.ExpiryTime - int64(days*24*60*60000)
							}
						}
						
						// Use ClientService to set expiry time (new architecture)
						clientEntity.ExpiryTime = date
						clientEntity.UpdatedAt = time.Now().Unix()
						
						clientService := ClientService{}
						needRestart, err := clientService.UpdateClient(userId, clientEntity)
						if needRestart {
							t.xrayService.SetToNeedRestart()
						}
						if err == nil {
							t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.expireResetSuccess", "Email=="+email))
							t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
							return
						}
					}
				}
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "reset_exp_in":
				if len(dataArray) >= 3 {
					oldInputNumber, err := strconv.Atoi(dataArray[2])
					inputNumber := oldInputNumber
					if err == nil {
						if len(dataArray) == 4 {
							num, err := strconv.Atoi(dataArray[3])
							if err == nil {
								switch num {
								case -2:
									inputNumber = 0
								case -1:
									if inputNumber > 0 {
										inputNumber = (inputNumber / 10)
									}
								default:
									inputNumber = (inputNumber * 10) + num
								}
							}
							if inputNumber == oldInputNumber {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
								return
							}
							if inputNumber >= 999999 {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
								return
							}
						}
						inlineKeyboard := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmNumber", "Num=="+strconv.Itoa(inputNumber))).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" "+strconv.Itoa(inputNumber))),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 1")),
								tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 2")),
								tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 3")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("4").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 4")),
								tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 5")),
								tu.InlineKeyboardButton("6").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 6")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("7").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 7")),
								tu.InlineKeyboardButton("8").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 8")),
								tu.InlineKeyboardButton("9").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 9")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("🔄").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" -2")),
								tu.InlineKeyboardButton("0").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 0")),
								tu.InlineKeyboardButton("⬅️").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" -1")),
							),
						)
						t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
						return
					}
				}
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "add_client_reset_exp_c":
				client_ExpiryTime = 0
				days, _ := strconv.ParseInt(dataArray[1], 10, 64)
				var date int64
				if client_ExpiryTime > 0 {
					if client_ExpiryTime-time.Now().Unix()*1000 < 0 {
						date = -int64(days * 24 * 60 * 60000)
					} else {
						date = client_ExpiryTime + int64(days*24*60*60000)
					}
				} else {
					date = client_ExpiryTime - int64(days*24*60*60000)
				}
				client_ExpiryTime = date

				messageId := callbackQuery.Message.GetMessageID()
				inbound, err := t.inboundService.GetInbound(receiver_inbound_ID)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}

				t.addClient(callbackQuery.Message.GetChat().ID, message_text, messageId)
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
			case "add_client_hwid_c":
				// Format: add_client_hwid_c maxHwid enabled
				if len(dataArray) >= 3 {
					maxHwid, err1 := strconv.Atoi(dataArray[1])
					enabled := dataArray[2] == "true"
					if err1 == nil {
						client_MaxHWID = maxHwid
						client_HWIDEnabled = enabled
						messageId := callbackQuery.Message.GetMessageID()
						inbound, err := t.inboundService.GetInbound(receiver_inbound_ID)
						if err != nil {
							t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
							return
						}
						message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
						if err != nil {
							t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
							return
						}
						t.addClient(chatId, message_text, messageId)
						t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
					}
				}
			case "add_client_reset_exp_in":
				if len(dataArray) >= 2 {
					oldInputNumber, err := strconv.Atoi(dataArray[1])
					inputNumber := oldInputNumber
					if err == nil {
						if len(dataArray) == 3 {
							num, err := strconv.Atoi(dataArray[2])
							if err == nil {
								switch num {
								case -2:
									inputNumber = 0
								case -1:
									if inputNumber > 0 {
										inputNumber = (inputNumber / 10)
									}
								default:
									inputNumber = (inputNumber * 10) + num
								}
							}
							if inputNumber == oldInputNumber {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
								return
							}
							if inputNumber >= 999999 {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
								return
							}
						}
						inlineKeyboard := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("add_client_default_traffic_exp")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmNumberAdd", "Num=="+strconv.Itoa(inputNumber))).WithCallbackData(t.encodeQuery("add_client_reset_exp_c "+strconv.Itoa(inputNumber))),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 1")),
								tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 2")),
								tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 3")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("4").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 4")),
								tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 5")),
								tu.InlineKeyboardButton("6").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 6")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("7").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 7")),
								tu.InlineKeyboardButton("8").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 8")),
								tu.InlineKeyboardButton("9").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 9")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("🔄").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" -2")),
								tu.InlineKeyboardButton("0").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 0")),
								tu.InlineKeyboardButton("⬅️").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" -1")),
							),
						)
						t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
						return
					}
				}
			case "remove_device":
				// device_list email hwidId
				if len(dataArray) >= 3 {
					hwidIdStr := dataArray[2]
					hwidId, err := strconv.Atoi(hwidIdStr)
					if err == nil {
						hwidService := ClientHWIDService{}
						err := hwidService.RemoveHWID(hwidId)
						if err == nil {
							t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.deviceRemovedSuccess"))
							t.searchClientDevices(chatId, email, callbackQuery.Message.GetMessageID())
						} else {
							t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
						}
					} else {
						t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
					}
				} else {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				}
			case "device_list":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.getDeviceList", "Email=="+email))
				t.searchClientDevices(chatId, email, callbackQuery.Message.GetMessageID())
			case "tg_user":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.getUserInfo", "Email=="+email))
				t.clientTelegramUserInfo(chatId, email)
			case "tgid_remove":
				inlineKeyboard := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("tgid_cancel "+email)),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmRemoveTGUser")).WithCallbackData(t.encodeQuery("tgid_remove_c "+email)),
					),
				)
				t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
			case "tgid_remove_c":
				// Use ClientService to remove TgID directly (new architecture)
				clientEntity, userId, err := t.getClientByEmailWithUserId(email)
				if err != nil {
					if err == gorm.ErrRecordNotFound {
						t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
						return
					}
					logger.Warning(err)
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
					return
				}
				
				// Update TgID to 0
				clientEntity.TgID = EmptyTelegramUserID
				clientEntity.UpdatedAt = time.Now().Unix()
				
				clientService := ClientService{}
				needRestart, err := clientService.UpdateClient(userId, clientEntity)
				if needRestart {
					t.xrayService.SetToNeedRestart()
				}
				if err == nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.removedTGUserSuccess", "Email=="+email))
					t.clientTelegramUserInfo(chatId, email, callbackQuery.Message.GetMessageID())
				} else {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				}
			case "toggle_enable":
				inlineKeyboard := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmToggle")).WithCallbackData(t.encodeQuery("toggle_enable_c "+email)),
					),
				)
				t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
			case "toggle_enable_c":
				// Use ClientService to toggle enable status (new architecture)
				clientEntity, userId, err := t.getClientByEmailWithUserId(email)
				if err != nil {
					if err == gorm.ErrRecordNotFound {
						t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
						return
					}
					logger.Warning(err)
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
					return
				}
				
				// Toggle enable status
				clientEntity.Enable = !clientEntity.Enable
				clientEntity.UpdatedAt = time.Now().Unix()
				
				clientService := ClientService{}
				needRestart, err := clientService.UpdateClient(userId, clientEntity)
				if needRestart {
					t.xrayService.SetToNeedRestart()
				}
				enabled := clientEntity.Enable
				if err == nil {
					if enabled {
						t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.enableSuccess", "Email=="+email))
					} else {
						t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.disableSuccess", "Email=="+email))
					}
					t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
				} else {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				}
			case "get_clients":
				inboundId := dataArray[1]
				inboundIdInt, err := strconv.Atoi(inboundId)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				inbound, err := t.inboundService.GetInbound(inboundIdInt)
				if err != nil || inbound == nil {
					logger.Warningf("Error getting inbound %d: %v", inboundIdInt, err)
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.wentWrong"))
					return
				}
				clients, err := t.getInboundClients(inboundIdInt)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				// Edit message instead of sending new one, fallback to send if edit fails
				err = t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), t.I18nBot("tgbot.answers.chooseClient", "Inbound=="+inbound.Remark), clients)
				if err != nil {
					t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.chooseClient", "Inbound=="+inbound.Remark), clients)
				}
			case "add_client_to":
				// assign default values to clients variables - reset all to ensure clean state
				client_Id = uuid.New().String()
				client_Flow = ""
				client_Email = strings.ToLower(t.randomLowerAndNum(8)) // Ensure lowercase
				client_TotalGB = 0
				client_ExpiryTime = 0
				client_Enable = true
				client_TgID = ""
				client_SubID = t.randomLowerAndNum(16)
				client_Comment = ""
				client_Reset = 0
				client_MaxHWID = 0
				client_HWIDEnabled = false
				client_Security = "auto"
				client_ShPassword = t.randomShadowSocksPassword()
				client_TrPassword = t.randomLowerAndNum(10)
				client_Method = ""
				logger.Debugf("add_client_to: Initialized new client with email '%s' for inbound %d", client_Email, receiver_inbound_ID)

				inboundId := dataArray[1]
				inboundIdInt, err := strconv.Atoi(inboundId)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				receiver_inbound_ID = inboundIdInt
				inbound, err := t.inboundService.GetInbound(inboundIdInt)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}

				message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}

				t.addClient(callbackQuery.Message.GetChat().ID, message_text, callbackQuery.Message.GetMessageID())
			}
			return
		} else {
			switch callbackQuery.Data {
			case "get_inbounds":
				inbounds, err := t.getInbounds()
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return

				}
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.allClients"))
				// Always edit message instead of sending new one
				// This ensures we don't create multiple messages when navigating
				err = t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), t.I18nBot("tgbot.answers.chooseInbound"), inbounds)
				if err != nil {
					// Only send new message if edit truly fails (shouldn't happen in normal flow)
					logger.Debugf("Failed to edit message for get_inbounds, sending new: %v", err)
					t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.chooseInbound"), inbounds)
				}
			case "admin_client_sub_links":
				inbounds, err := t.getInboundsFor("get_clients_for_sub")
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				err = t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), t.I18nBot("tgbot.answers.chooseInbound"), inbounds)
				if err != nil {
					t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.chooseInbound"), inbounds)
				}
			case "admin_client_individual_links":
				inbounds, err := t.getInboundsFor("get_clients_for_individual")
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				err = t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), t.I18nBot("tgbot.answers.chooseInbound"), inbounds)
				if err != nil {
					t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.chooseInbound"), inbounds)
				}
			case "admin_client_qr_links":
				inbounds, err := t.getInboundsFor("get_clients_for_qr")
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				err = t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), t.I18nBot("tgbot.answers.chooseInbound"), inbounds)
				if err != nil {
					t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.chooseInbound"), inbounds)
				}
			}

		}
	}

	switch callbackQuery.Data {
	case "back_to_main":
		// Return to main menu - use full menu from SendAnswer
		t.sendCallbackAnswerTgBot(callbackQuery.ID, "")
		mainMenuText := t.I18nBot("tgbot.commands.pleaseChoose")
		
		// Build full menu same as SendAnswer
		var mainMenu *telego.InlineKeyboardMarkup
		if isAdmin {
			mainMenu = tu.InlineKeyboard(
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.SortedTrafficUsageReport")).WithCallbackData(t.encodeQuery("get_sorted_traffic_usage_report")),
				),
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.serverUsage")).WithCallbackData(t.encodeQuery("get_usage")),
					tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.ResetAllTraffics")).WithCallbackData(t.encodeQuery("reset_all_traffics")),
				),
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.dbBackup")).WithCallbackData(t.encodeQuery("get_backup")),
				),
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.getInbounds")).WithCallbackData(t.encodeQuery("inbounds")),
					tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.depleteSoon")).WithCallbackData(t.encodeQuery("deplete_soon")),
				),
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.commands")).WithCallbackData(t.encodeQuery("commands")),
					tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.onlines")).WithCallbackData(t.encodeQuery("onlines")),
				),
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.allClients")).WithCallbackData(t.encodeQuery("get_inbounds")),
					tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.addClient")).WithCallbackData(t.encodeQuery("add_client")),
				),
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton(t.I18nBot("pages.settings.subSettings")).WithCallbackData(t.encodeQuery("admin_client_sub_links")),
					tu.InlineKeyboardButton(t.I18nBot("subscription.individualLinks")).WithCallbackData(t.encodeQuery("admin_client_individual_links")),
					tu.InlineKeyboardButton(t.I18nBot("qrCode")).WithCallbackData(t.encodeQuery("admin_client_qr_links")),
				),
			)
		} else {
			mainMenu = tu.InlineKeyboard(
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.clientUsage")).WithCallbackData(t.encodeQuery("client_traffic")),
					tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.commands")).WithCallbackData(t.encodeQuery("client_commands")),
				),
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton(t.I18nBot("pages.settings.subSettings")).WithCallbackData(t.encodeQuery("client_sub_links")),
					tu.InlineKeyboardButton(t.I18nBot("subscription.individualLinks")).WithCallbackData(t.encodeQuery("client_individual_links")),
				),
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton(t.I18nBot("qrCode")).WithCallbackData(t.encodeQuery("client_qr_links")),
				),
			)
		}
		
		err := t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), mainMenuText, mainMenu)
		if err != nil {
			// If edit fails, send new message with menu
			t.SendAnswer(chatId, mainMenuText, isAdmin)
		}
		return
	case "get_usage":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.serverUsage"))
		// Edit message instead of sending new one
		t.getServerUsage(chatId, callbackQuery.Message.GetMessageID())
	case "usage_refresh":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
		t.getServerUsage(chatId, callbackQuery.Message.GetMessageID())
	case "inbounds":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.getInbounds"))
		// Edit message instead of sending new one
		info := t.getInboundUsages()
		// Add back button to main menu
		backKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("back_to_main")),
			),
		)
		err := t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), info, backKeyboard)
		if err != nil {
			t.SendMsgToTgbot(chatId, info, backKeyboard)
		}
	case "deplete_soon":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.depleteSoon"))
		// Edit message instead of sending new one
		t.getExhausted(chatId, callbackQuery.Message.GetMessageID())
	case "get_backup":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.dbBackup"))
		// Backup sends file, so we can't edit - send new message is OK
		t.sendBackup(chatId)
	case "client_traffic":
		tgUserID := callbackQuery.From.ID
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.clientUsage"))
		// Edit message instead of sending new one
		t.getClientUsage(chatId, tgUserID, callbackQuery.Message.GetMessageID())
	case "client_commands":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.commands"))
		// Edit message instead of sending new one
		helpText := t.I18nBot("tgbot.commands.helpClientCommands")
		// Add back button to main menu (for client users)
		backKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData("back_to_main"),
			),
		)
		err := t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), helpText, backKeyboard)
		if err != nil {
			t.SendMsgToTgbot(chatId, helpText, backKeyboard)
		}
	case "client_sub_links":
		// show user's own clients to choose one for sub links
		tgUserID := callbackQuery.From.ID
		// Try new architecture first
		traffics, err := t.getClientTrafficTgBotNew(tgUserID)
		if err != nil || len(traffics) == 0 {
			// Fallback to old method
			traffics, err = t.inboundService.GetClientTrafficTgBot(tgUserID)
			if err != nil {
				t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation")+"\r\n"+err.Error())
				return
			}
		}
		if len(traffics) == 0 {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.askToAddUserId", "TgUserID=="+strconv.FormatInt(tgUserID, 10)))
			return
		}
		var buttons []telego.InlineKeyboardButton
		for _, tr := range traffics {
			buttons = append(buttons, tu.InlineKeyboardButton(tr.Email).WithCallbackData(t.encodeQuery("client_sub_links "+tr.Email)))
		}
		cols := 1
		if len(buttons) >= 6 {
			cols = 2
		}
		// Add back button to main menu (for client users)
		backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData("back_to_main")
		buttons = append(buttons, backButton)
		keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols, buttons...))
		// Edit message instead of sending new one
		err = t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), t.I18nBot("tgbot.commands.pleaseChoose"), keyboard)
		if err != nil {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.commands.pleaseChoose"), keyboard)
		}
	case "client_individual_links":
		// show user's clients to choose for individual links
		tgUserID := callbackQuery.From.ID
		// Try new architecture first
		traffics, err := t.getClientTrafficTgBotNew(tgUserID)
		if err != nil || len(traffics) == 0 {
			// Fallback to old method
			traffics, err = t.inboundService.GetClientTrafficTgBot(tgUserID)
			if err != nil {
				t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation")+"\r\n"+err.Error())
				return
			}
		}
		if len(traffics) == 0 {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.askToAddUserId", "TgUserID=="+strconv.FormatInt(tgUserID, 10)))
			return
		}
		var buttons2 []telego.InlineKeyboardButton
		for _, tr := range traffics {
			buttons2 = append(buttons2, tu.InlineKeyboardButton(tr.Email).WithCallbackData(t.encodeQuery("client_individual_links "+tr.Email)))
		}
		cols2 := 1
		if len(buttons2) >= 6 {
			cols2 = 2
		}
		// Add back button to main menu (for client users)
		backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData("back_to_main")
		buttons2 = append(buttons2, backButton)
		keyboard2 := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols2, buttons2...))
		// Edit message instead of sending new one
		err = t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), t.I18nBot("tgbot.commands.pleaseChoose"), keyboard2)
		if err != nil {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.commands.pleaseChoose"), keyboard2)
		}
	case "client_qr_links":
		// show user's clients to choose for QR codes
		tgUserID := callbackQuery.From.ID
		// Try new architecture first
		traffics, err := t.getClientTrafficTgBotNew(tgUserID)
		if err != nil || len(traffics) == 0 {
			// Fallback to old method
			traffics, err = t.inboundService.GetClientTrafficTgBot(tgUserID)
			if err != nil {
				t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOccurred")+"\r\n"+err.Error())
				return
			}
		}
		if len(traffics) == 0 {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.askToAddUserId", "TgUserID=="+strconv.FormatInt(tgUserID, 10)))
			return
		}
		var buttons3 []telego.InlineKeyboardButton
		for _, tr := range traffics {
			buttons3 = append(buttons3, tu.InlineKeyboardButton(tr.Email).WithCallbackData(t.encodeQuery("client_qr_links "+tr.Email)))
		}
		cols3 := 1
		if len(buttons3) >= 6 {
			cols3 = 2
		}
		// Add back button to main menu (for client users)
		backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData("back_to_main")
		buttons3 = append(buttons3, backButton)
		keyboard3 := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols3, buttons3...))
		// Edit message instead of sending new one
		err = t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), t.I18nBot("tgbot.commands.pleaseChoose"), keyboard3)
		if err != nil {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.commands.pleaseChoose"), keyboard3)
		}
	case "onlines":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.onlines"))
		// Edit message instead of sending new one
		t.onlineClients(chatId, callbackQuery.Message.GetMessageID())
	case "onlines_refresh":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
		t.onlineClients(chatId, callbackQuery.Message.GetMessageID())
	case "commands":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.commands"))
		// Edit message instead of sending new one
		helpText := t.I18nBot("tgbot.commands.helpAdminCommands")
		// Add back button to main menu
		backKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("back_to_main")),
			),
		)
		err := t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), helpText, backKeyboard)
		if err != nil {
			t.SendMsgToTgbot(chatId, helpText, backKeyboard)
		}
	case "add_client":
		// assign default values to clients variables - reset all to ensure clean state
		client_Id = uuid.New().String()
		client_Flow = ""
		client_Email = strings.ToLower(t.randomLowerAndNum(8)) // Ensure lowercase
		client_TotalGB = 0
		client_ExpiryTime = 0
		client_Enable = true
		client_TgID = ""
		client_SubID = t.randomLowerAndNum(16)
		client_Comment = ""
		client_Reset = 0
		client_Security = "auto"
		client_ShPassword = t.randomShadowSocksPassword()
		client_TrPassword = t.randomLowerAndNum(10)
		client_Method = ""
		client_MaxHWID = 0
		client_HWIDEnabled = false
		logger.Debugf("add_client: Initialized new client with email '%s'", client_Email)

		inbounds, err := t.getInboundsAddClient()
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
			return
		}
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.addClient"))
		// Edit message instead of sending new one
		err = t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), t.I18nBot("tgbot.answers.chooseInbound"), inbounds)
		if err != nil {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.chooseInbound"), inbounds)
		}
	case "add_client_ch_default_email":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		userStates[chatId] = "awaiting_email"
		cancel_btn_markup := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
			),
		)
		prompt_message := t.I18nBot("tgbot.messages.email_prompt", "ClientEmail=="+client_Email)
		t.SendMsgToTgbot(chatId, prompt_message, cancel_btn_markup)
	case "add_client_ch_default_id":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		userStates[chatId] = "awaiting_id"
		cancel_btn_markup := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
			),
		)
		prompt_message := t.I18nBot("tgbot.messages.id_prompt", "ClientId=="+client_Id)
		t.SendMsgToTgbot(chatId, prompt_message, cancel_btn_markup)
	case "add_client_ch_default_pass_tr":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		userStates[chatId] = "awaiting_password_tr"
		cancel_btn_markup := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
			),
		)
		prompt_message := t.I18nBot("tgbot.messages.pass_prompt", "ClientPassword=="+client_TrPassword)
		t.SendMsgToTgbot(chatId, prompt_message, cancel_btn_markup)
	case "add_client_ch_default_pass_sh":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		userStates[chatId] = "awaiting_password_sh"
		cancel_btn_markup := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
			),
		)
		prompt_message := t.I18nBot("tgbot.messages.pass_prompt", "ClientPassword=="+client_ShPassword)
		t.SendMsgToTgbot(chatId, prompt_message, cancel_btn_markup)
	case "add_client_ch_default_comment":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		userStates[chatId] = "awaiting_comment"
		cancel_btn_markup := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
			),
		)
		prompt_message := t.I18nBot("tgbot.messages.comment_prompt", "ClientComment=="+client_Comment)
		t.SendMsgToTgbot(chatId, prompt_message, cancel_btn_markup)
	case "add_client_ch_default_traffic":
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("add_client_default_traffic_exp")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.unlimited")).WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 0")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.custom")).WithCallbackData(t.encodeQuery("add_client_limit_traffic_in 0")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("1 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 1")),
				tu.InlineKeyboardButton("5 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 5")),
				tu.InlineKeyboardButton("10 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 10")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("20 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 20")),
				tu.InlineKeyboardButton("30 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 30")),
				tu.InlineKeyboardButton("40 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 40")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("50 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 50")),
				tu.InlineKeyboardButton("60 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 60")),
				tu.InlineKeyboardButton("80 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 80")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("100 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 100")),
				tu.InlineKeyboardButton("150 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 150")),
				tu.InlineKeyboardButton("200 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 200")),
			),
		)
		t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
	case "add_client_ch_default_exp":
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("add_client_default_traffic_exp")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.unlimited")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 0")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.custom")).WithCallbackData(t.encodeQuery("add_client_reset_exp_in 0")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 7 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 7")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 10 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 10")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 14 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 14")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 20 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 20")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 1 "+t.I18nBot("tgbot.month")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 30")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 3 "+t.I18nBot("tgbot.months")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 90")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 6 "+t.I18nBot("tgbot.months")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 180")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 12 "+t.I18nBot("tgbot.months")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 365")),
			),
		)
		t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
	case "add_client_ch_default_hwid":
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("add_client_default_hwid")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.disable")).WithCallbackData(t.encodeQuery("add_client_hwid_c 0 false")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.unlimited")).WithCallbackData(t.encodeQuery("add_client_hwid_c 0 true")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("add_client_hwid_c 1 true")),
				tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("add_client_hwid_c 2 true")),
				tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("add_client_hwid_c 3 true")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("add_client_hwid_c 5 true")),
				tu.InlineKeyboardButton("10").WithCallbackData(t.encodeQuery("add_client_hwid_c 10 true")),
			),
		)
		t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
	case "add_client_default_info":
		messageId := callbackQuery.Message.GetMessageID()
		t.deleteMessageTgBot(chatId, messageId)
		t.SendMsgToTgbotDeleteAfter(chatId, t.I18nBot("tgbot.messages.using_default_value"), 3, tu.ReplyKeyboardRemove())
		delete(userStates, chatId)
		inbound, _ := t.inboundService.GetInbound(receiver_inbound_ID)
		message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
		// Note: messageId is not available here as message was deleted, so we send new message
		t.addClient(chatId, message_text)
	case "add_client_back_to_form":
		// Return to client form after error
		t.sendCallbackAnswerTgBot(callbackQuery.ID, "")
		inbound, err := t.inboundService.GetInbound(receiver_inbound_ID)
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
			return
		}
		message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
			return
		}
		t.addClient(chatId, message_text, callbackQuery.Message.GetMessageID())
	case "add_client_cancel":
		delete(userStates, chatId)
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.SendMsgToTgbotDeleteAfter(chatId, t.I18nBot("tgbot.messages.cancel"), 3, tu.ReplyKeyboardRemove())
	case "add_client_default_traffic_exp":
		messageId := callbackQuery.Message.GetMessageID()
		inbound, err := t.inboundService.GetInbound(receiver_inbound_ID)
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
			return
		}
		message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
			return
		}
		t.addClient(chatId, message_text, messageId)
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.canceled", "Email=="+client_Email))
	case "add_client_default_hwid":
		messageId := callbackQuery.Message.GetMessageID()
		inbound, err := t.inboundService.GetInbound(receiver_inbound_ID)
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
			return
		}
		message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
			return
		}
		t.addClient(chatId, message_text, messageId)
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.canceled", "Email=="+client_Email))
	case "add_client_submit_disable":
		client_Enable = false
		_, err := t.SubmitAddClient()
		messageId := callbackQuery.Message.GetMessageID()
		if err != nil {
			errorMessage := fmt.Sprintf("%v", err)
			errorMsg := t.I18nBot("tgbot.messages.error_add_client", "error=="+errorMessage)
			// Add back button to return to form
			inbound, inboundErr := t.inboundService.GetInbound(receiver_inbound_ID)
			if inboundErr == nil {
				message_text, buildErr := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				if buildErr == nil {
					// Show error with back button that returns to form
					backKeyboard := tu.InlineKeyboard(
						tu.InlineKeyboardRow(
							tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("add_client_back_to_form")),
						),
					)
					t.editMessageTgBot(chatId, messageId, errorMsg+"\r\n\r\n"+message_text, backKeyboard)
				} else {
					t.editMessageTgBot(chatId, messageId, errorMsg)
				}
			} else {
				t.editMessageTgBot(chatId, messageId, errorMsg)
			}
		} else {
			// Client created successfully - edit message with success notification and navigation buttons
			successMsg := t.I18nBot("tgbot.answers.successfulOperation")
			// Add navigation buttons
			backKeyboard := tu.InlineKeyboard(
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("back_to_main")),
				),
			)
			t.editMessageTgBot(chatId, messageId, successMsg, backKeyboard)
		}
	case "add_client_submit_enable":
		client_Enable = true
		_, err := t.SubmitAddClient()
		messageId := callbackQuery.Message.GetMessageID()
		if err != nil {
			errorMessage := fmt.Sprintf("%v", err)
			errorMsg := t.I18nBot("tgbot.messages.error_add_client", "error=="+errorMessage)
			// Add back button to return to form
			inbound, inboundErr := t.inboundService.GetInbound(receiver_inbound_ID)
			if inboundErr == nil {
				message_text, buildErr := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				if buildErr == nil {
					// Show error with back button that returns to form
					backKeyboard := tu.InlineKeyboard(
						tu.InlineKeyboardRow(
							tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("add_client_back_to_form")),
						),
					)
					t.editMessageTgBot(chatId, messageId, errorMsg+"\r\n\r\n"+message_text, backKeyboard)
				} else {
					t.editMessageTgBot(chatId, messageId, errorMsg)
				}
			} else {
				t.editMessageTgBot(chatId, messageId, errorMsg)
			}
		} else {
			// Client created successfully - edit message with success notification and navigation buttons
			successMsg := t.I18nBot("tgbot.answers.successfulOperation")
			// Add navigation buttons
			backKeyboard := tu.InlineKeyboard(
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("back_to_main")),
				),
			)
			t.editMessageTgBot(chatId, messageId, successMsg, backKeyboard)
		}
	case "reset_all_traffics_cancel":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.SendMsgToTgbotDeleteAfter(chatId, t.I18nBot("tgbot.messages.cancel"), 1, tu.ReplyKeyboardRemove())
	case "reset_all_traffics":
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancelReset")).WithCallbackData(t.encodeQuery("reset_all_traffics_cancel")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmResetTraffic")).WithCallbackData(t.encodeQuery("reset_all_traffics_c")),
			),
		)
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.messages.AreYouSure"), inlineKeyboard)
	case "reset_all_traffics_c":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		emails, err := t.inboundService.getAllEmails()
		if err != nil {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation"), tu.ReplyKeyboardRemove())
			return
		}

		for _, email := range emails {
			err := t.inboundService.ResetClientTrafficByEmail(email)
			if err == nil {
				msg := t.I18nBot("tgbot.messages.SuccessResetTraffic", "ClientEmail=="+email)
				t.SendMsgToTgbot(chatId, msg, tu.ReplyKeyboardRemove())
			} else {
				msg := t.I18nBot("tgbot.messages.FailedResetTraffic", "ClientEmail=="+email, "ErrorMessage=="+err.Error())
				t.SendMsgToTgbot(chatId, msg, tu.ReplyKeyboardRemove())
			}
		}

		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.messages.FinishProcess"), tu.ReplyKeyboardRemove())
	case "get_sorted_traffic_usage_report":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		emails, err := t.inboundService.getAllEmails()

		if err != nil {
			errorKeyboard := t.createBackAndDeleteKeyboard(chatId, 0)
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation"), errorKeyboard)
			return
		}
		valid_emails, extra_emails, err := t.inboundService.FilterAndSortClientEmails(emails)
		if err != nil {
			errorKeyboard := t.createBackAndDeleteKeyboard(chatId, 0)
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation"), errorKeyboard)
			return
		}

		for _, valid_emails := range valid_emails {
			// Use ClientService to get client data (new architecture)
			clientEntity, _, err := t.getClientByEmailWithUserId(valid_emails)
			if err != nil {
				if err == gorm.ErrRecordNotFound {
					msg := fmt.Sprintf("📧 %s\n%s", valid_emails, t.I18nBot("tgbot.noResult"))
					keyboard := t.createBackAndDeleteKeyboard(chatId, 0)
					sentMsg, err := t.sendMessageWithKeyboard(chatId, msg, keyboard)
					if err == nil && sentMsg != nil {
						// Update keyboard with actual message ID
						keyboard = t.createBackAndDeleteKeyboard(chatId, sentMsg.MessageID)
						t.editMessageTgBot(chatId, sentMsg.MessageID, msg, keyboard)
					}
					continue
				}
				logger.Warning(err)
				msg := t.I18nBot("tgbot.wentWrong")
				keyboard := t.createBackAndDeleteKeyboard(chatId, 0)
				sentMsg, err := t.sendMessageWithKeyboard(chatId, msg, keyboard)
				if err == nil && sentMsg != nil {
					// Update keyboard with actual message ID
					keyboard = t.createBackAndDeleteKeyboard(chatId, sentMsg.MessageID)
					t.editMessageTgBot(chatId, sentMsg.MessageID, msg, keyboard)
				}
				continue
			}

			// Create ClientTraffic from ClientEntity for compatibility with clientInfoMsg
			totalBytes := int64(clientEntity.TotalGB * 1024 * 1024 * 1024)
			traffic := &xray.ClientTraffic{
				Id:         0,
				InboundId:  0,
				Enable:     clientEntity.Enable,
				Email:      clientEntity.Email,
				UUID:       clientEntity.UUID,
				SubId:      clientEntity.SubID,
				Up:         clientEntity.Up,
				Down:       clientEntity.Down,
				AllTime:    clientEntity.AllTime,
				ExpiryTime: clientEntity.ExpiryTime,
				Total:      totalBytes,
				Reset:      clientEntity.Reset,
				LastOnline: clientEntity.LastOnline,
			}

			output := t.clientInfoMsg(traffic, false, false, false, false, true, false)
			keyboard := t.createBackAndDeleteKeyboard(chatId, 0)
			sentMsg, err := t.sendMessageWithKeyboard(chatId, output, keyboard)
			if err == nil && sentMsg != nil {
				// Update keyboard with actual message ID
				keyboard = t.createBackAndDeleteKeyboard(chatId, sentMsg.MessageID)
				t.editMessageTgBot(chatId, sentMsg.MessageID, output, keyboard)
			}
		}
		for _, extra_emails := range extra_emails {
			msg := fmt.Sprintf("📧 %s\n%s", extra_emails, t.I18nBot("tgbot.noResult"))
			keyboard := t.createBackAndDeleteKeyboard(chatId, 0)
			sentMsg, err := t.sendMessageWithKeyboard(chatId, msg, keyboard)
			if err == nil && sentMsg != nil {
				// Update keyboard with actual message ID
				keyboard = t.createBackAndDeleteKeyboard(chatId, sentMsg.MessageID)
				t.editMessageTgBot(chatId, sentMsg.MessageID, msg, keyboard)
			}
		}
	default:
		if after, ok := strings.CutPrefix(callbackQuery.Data, "client_sub_links "); ok {
			email := after
			t.sendClientSubLinks(chatId, email, callbackQuery.Message.GetMessageID())
			return
		}
		if after, ok := strings.CutPrefix(callbackQuery.Data, "client_individual_links "); ok {
			email := after
			t.sendClientIndividualLinks(chatId, email, callbackQuery.Message.GetMessageID())
			return
		}
		if after, ok := strings.CutPrefix(callbackQuery.Data, "client_qr_links "); ok {
			email := after
			t.sendClientQRLinks(chatId, email, callbackQuery.Message.GetMessageID())
			return
		}
		// Handle delete_message callback with encoded query
		decodedQuery, err := t.decodeQuery(callbackQuery.Data)
		if err == nil {
			if strings.HasPrefix(decodedQuery, "delete_message ") {
				parts := strings.Fields(decodedQuery)
				if len(parts) >= 2 {
					msgID, err := strconv.Atoi(parts[1])
					if err == nil {
						t.deleteMessageTgBot(chatId, msgID)
						t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
					}
				}
				return
			}
		}
	}
}

// BuildInboundClientDataMessage builds a message with client data for the given inbound and protocol.
func (t *Tgbot) BuildInboundClientDataMessage(inbound_remark string, protocol model.Protocol) (string, error) {
	var message string

	currentTime := time.Now()
	timestampMillis := currentTime.UnixNano() / int64(time.Millisecond)

	expiryTime := ""
	diff := client_ExpiryTime/1000 - timestampMillis
	if client_ExpiryTime == 0 {
		expiryTime = t.I18nBot("tgbot.unlimited")
	} else if diff > 172800 {
		expiryTime = time.Unix((client_ExpiryTime / 1000), 0).Format("2006-01-02 15:04:05")
	} else if client_ExpiryTime < 0 {
		expiryTime = fmt.Sprintf("%d %s", client_ExpiryTime/-86400000, t.I18nBot("tgbot.days"))
	} else {
		expiryTime = fmt.Sprintf("%d %s", diff/3600, t.I18nBot("tgbot.hours"))
	}

	traffic_value := ""
	if client_TotalGB == 0 {
		traffic_value = "♾️ Unlimited(Reset)"
	} else {
		traffic_value = common.FormatTraffic(client_TotalGB)
	}

	switch protocol {
	case model.VMESS, model.VLESS:
		message = t.I18nBot("tgbot.messages.inbound_client_data_id", "InboundRemark=="+inbound_remark, "ClientId=="+client_Id, "ClientEmail=="+client_Email, "ClientTraffic=="+traffic_value, "ClientExp=="+expiryTime, "ClientComment=="+client_Comment)

	case model.Trojan:
		message = t.I18nBot("tgbot.messages.inbound_client_data_pass", "InboundRemark=="+inbound_remark, "ClientPass=="+client_TrPassword, "ClientEmail=="+client_Email, "ClientTraffic=="+traffic_value, "ClientExp=="+expiryTime, "ClientComment=="+client_Comment)

	case model.Shadowsocks:
		message = t.I18nBot("tgbot.messages.inbound_client_data_pass", "InboundRemark=="+inbound_remark, "ClientPass=="+client_ShPassword, "ClientEmail=="+client_Email, "ClientTraffic=="+traffic_value, "ClientExp=="+expiryTime, "ClientComment=="+client_Comment)

	default:
		return "", errors.New("unknown protocol")
	}

	return message, nil
}

// BuildJSONForProtocol builds a JSON string for the given protocol with client data.
func (t *Tgbot) BuildJSONForProtocol(protocol model.Protocol) (string, error) {
	var jsonString string

	switch protocol {
	case model.VMESS:
		jsonString = fmt.Sprintf(`{
            "clients": [{
                "id": "%s",
                "security": "%s",
                "email": "%s",
                "totalGB": %d,
                "expiryTime": %d,
                "enable": %t,
                "tgId": "%s",
                "subId": "%s",
                "comment": "%s",
                "reset": %d,
                "hwidEnabled": %t,
                "maxHwid": %d
            }]
        }`, client_Id, client_Security, client_Email, client_TotalGB, client_ExpiryTime, client_Enable, client_TgID, client_SubID, client_Comment, client_Reset, client_HWIDEnabled, client_MaxHWID)

	case model.VLESS:
		jsonString = fmt.Sprintf(`{
            "clients": [{
                "id": "%s",
                "flow": "%s",
                "email": "%s",
                "totalGB": %d,
                "expiryTime": %d,
                "enable": %t,
                "tgId": "%s",
                "subId": "%s",
                "comment": "%s",
                "reset": %d
            }]
        }`, client_Id, client_Flow, client_Email, client_TotalGB, client_ExpiryTime, client_Enable, client_TgID, client_SubID, client_Comment, client_Reset)

	case model.Trojan:
		jsonString = fmt.Sprintf(`{
            "clients": [{
                "password": "%s",
                "email": "%s",
                "totalGB": %d,
                "expiryTime": %d,
                "enable": %t,
                "tgId": "%s",
                "subId": "%s",
                "comment": "%s",
                "reset": %d
            }]
        }`, client_TrPassword, client_Email, client_TotalGB, client_ExpiryTime, client_Enable, client_TgID, client_SubID, client_Comment, client_Reset)

	case model.Shadowsocks:
		jsonString = fmt.Sprintf(`{
            "clients": [{
                "method": "%s",
                "password": "%s",
                "email": "%s",
                "totalGB": %d,
                "expiryTime": %d,
                "enable": %t,
                "tgId": "%s",
                "subId": "%s",
                "comment": "%s",
                "reset": %d
            }]
        }`, client_Method, client_ShPassword, client_Email, client_TotalGB, client_ExpiryTime, client_Enable, client_TgID, client_SubID, client_Comment, client_Reset)

	default:
		return "", errors.New("unknown protocol")
	}

	return jsonString, nil
}

// SubmitAddClient submits the client addition request using new architecture (ClientEntity).
func (t *Tgbot) SubmitAddClient() (bool, error) {
	inbound, err := t.inboundService.GetInbound(receiver_inbound_ID)
	if err != nil {
		logger.Warning("getIboundClients run failed:", err)
		return false, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}

	// Normalize email to lowercase
	client_Email = strings.ToLower(strings.TrimSpace(client_Email))
	
	// Log for debugging
	logger.Debugf("SubmitAddClient: Creating client with email '%s' for inbound %d (userId: %d)", client_Email, receiver_inbound_ID, inbound.UserId)

	// Create ClientEntity directly using new architecture
	clientService := ClientService{}
	
	// Convert TotalGB from bytes to GB (client_TotalGB is stored in bytes)
	totalGB := float64(0)
	if client_TotalGB > 0 {
		totalGB = float64(client_TotalGB) / (1024 * 1024 * 1024)
	}
	
	// Convert TgID from string to int64
	var tgID int64 = 0
	if client_TgID != "" {
		if parsedTgID, err := strconv.ParseInt(client_TgID, 10, 64); err == nil {
			tgID = parsedTgID
		}
	}
	
	// Build ClientEntity from global variables
	clientEntity := &model.ClientEntity{
		UserId:      inbound.UserId,
		Email:       client_Email,
		UUID:        client_Id,
		Security:    client_Security,
		Flow:        client_Flow,
		TotalGB:     totalGB,
		ExpiryTime:  client_ExpiryTime,
		Enable:      client_Enable,
		TgID:        tgID,
		SubID:       client_SubID,
		Comment:     client_Comment,
		Reset:       client_Reset,
		HWIDEnabled: client_HWIDEnabled,
		MaxHWID:     client_MaxHWID,
	}
	
	// Set password based on protocol
	switch inbound.Protocol {
	case model.Trojan:
		clientEntity.Password = client_TrPassword
	case model.Shadowsocks:
		clientEntity.Password = client_ShPassword
		// Method is stored in inbound settings, not in client
	case model.VMESS, model.VLESS:
		// Password not used for VMESS/VLESS
	}
	
	// Set inbound assignment
	clientEntity.InboundIds = []int{receiver_inbound_ID}

	// Add client using ClientService (new architecture)
	needRestart, err := clientService.AddClient(inbound.UserId, clientEntity)
	if err != nil {
		logger.Warningf("SubmitAddClient: Failed to add client: %v", err)
		return false, err
	}
	
	logger.Debugf("SubmitAddClient: Successfully added client with email '%s'", client_Email)
	return needRestart, nil
}

// checkAdmin checks if the given Telegram ID is an admin.
func checkAdmin(tgId int64) bool {
	for _, adminId := range adminIds {
		if adminId == tgId {
			return true
		}
	}
	return false
}

// SendAnswer sends a response message with an inline keyboard to the specified chat.
func (t *Tgbot) SendAnswer(chatId int64, msg string, isAdmin bool) {
	numericKeyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.SortedTrafficUsageReport")).WithCallbackData(t.encodeQuery("get_sorted_traffic_usage_report")),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.serverUsage")).WithCallbackData(t.encodeQuery("get_usage")),
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.ResetAllTraffics")).WithCallbackData(t.encodeQuery("reset_all_traffics")),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.dbBackup")).WithCallbackData(t.encodeQuery("get_backup")),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.getInbounds")).WithCallbackData(t.encodeQuery("inbounds")),
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.depleteSoon")).WithCallbackData(t.encodeQuery("deplete_soon")),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.commands")).WithCallbackData(t.encodeQuery("commands")),
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.onlines")).WithCallbackData(t.encodeQuery("onlines")),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.allClients")).WithCallbackData(t.encodeQuery("get_inbounds")),
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.addClient")).WithCallbackData(t.encodeQuery("add_client")),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("pages.settings.subSettings")).WithCallbackData(t.encodeQuery("admin_client_sub_links")),
			tu.InlineKeyboardButton(t.I18nBot("subscription.individualLinks")).WithCallbackData(t.encodeQuery("admin_client_individual_links")),
			tu.InlineKeyboardButton(t.I18nBot("qrCode")).WithCallbackData(t.encodeQuery("admin_client_qr_links")),
		),
		// TODOOOOOOOOOOOOOO: Add restart button here.
	)
	numericKeyboardClient := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.clientUsage")).WithCallbackData(t.encodeQuery("client_traffic")),
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.commands")).WithCallbackData(t.encodeQuery("client_commands")),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("pages.settings.subSettings")).WithCallbackData(t.encodeQuery("client_sub_links")),
			tu.InlineKeyboardButton(t.I18nBot("subscription.individualLinks")).WithCallbackData(t.encodeQuery("client_individual_links")),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("qrCode")).WithCallbackData(t.encodeQuery("client_qr_links")),
		),
	)

	var ReplyMarkup telego.ReplyMarkup
	if isAdmin {
		ReplyMarkup = numericKeyboard
	} else {
		ReplyMarkup = numericKeyboardClient
	}
	t.SendMsgToTgbot(chatId, msg, ReplyMarkup)
}

// SendMsgToTgbot sends a message to the Telegram bot with optional reply markup.
func (t *Tgbot) SendMsgToTgbot(chatId int64, msg string, replyMarkup ...telego.ReplyMarkup) {
	if !isRunning {
		return
	}

	if msg == "" {
		logger.Info("[tgbot] message is empty!")
		return
	}

	var allMessages []string
	limit := 2000

	// paging message if it is big
	if len(msg) > limit {
		messages := strings.Split(msg, "\r\n\r\n")
		lastIndex := -1

		for _, message := range messages {
			if (len(allMessages) == 0) || (len(allMessages[lastIndex])+len(message) > limit) {
				allMessages = append(allMessages, message)
				lastIndex++
			} else {
				allMessages[lastIndex] += "\r\n\r\n" + message
			}
		}
		if strings.TrimSpace(allMessages[len(allMessages)-1]) == "" {
			allMessages = allMessages[:len(allMessages)-1]
		}
	} else {
		allMessages = append(allMessages, msg)
	}
	for n, message := range allMessages {
		params := telego.SendMessageParams{
			ChatID:    tu.ID(chatId),
			Text:      message,
			ParseMode: "HTML",
		}
		// only add replyMarkup to last message
		if len(replyMarkup) > 0 && n == (len(allMessages)-1) {
			params.ReplyMarkup = replyMarkup[0]
		}
		_, err := bot.SendMessage(context.Background(), &params)
		if err != nil {
			logger.Warning("Error sending telegram message :", err)
		}
		// Reduced delay to improve performance (only needed for rate limiting)
		if n < len(allMessages)-1 { // Only delay between messages, not after the last one
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// buildSubscriptionURLs builds the HTML sub page URL and JSON subscription URL for a client email
func (t *Tgbot) buildSubscriptionURLs(email string) (string, string, error) {
	// Use ClientService to find client by email (new architecture)
	clientEntity, _, err := t.getClientByEmailWithUserId(email)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			logger.Warningf("Client not found for email: %s", email)
			return "", "", errors.New("клиент не найден")
		}
		logger.Warningf("Error getting client by email %s: %v", email, err)
		return "", "", fmt.Errorf("ошибка при получении клиента: %v", err)
	}
	
	if clientEntity.SubID == "" {
		logger.Warningf("Client %s has no SubID", email)
		return "", "", errors.New("у клиента не установлен Subscription ID")
	}

	// Gather settings to construct absolute URLs
	subDomain, _ := t.settingService.GetSubDomain()
	subPort, _ := t.settingService.GetSubPort()
	subPath, _ := t.settingService.GetSubPath()
	subJsonPath, _ := t.settingService.GetSubJsonPath()
	subJsonEnable, _ := t.settingService.GetSubJsonEnable()
	subKeyFile, _ := t.settingService.GetSubKeyFile()
	subCertFile, _ := t.settingService.GetSubCertFile()

	tls := (subKeyFile != "" && subCertFile != "")
	scheme := "http"
	if tls {
		scheme = "https"
	}

	// Fallbacks
	if subDomain == "" {
		// try panel domain, otherwise OS hostname
		if d, err := t.settingService.GetWebDomain(); err == nil && d != "" {
			subDomain = d
		} else if hostname != "" {
			subDomain = hostname
		} else {
			subDomain = "localhost"
		}
	}

	host := subDomain
	if (subPort == 443 && tls) || (subPort == 80 && !tls) {
		// standard ports: no port in host
	} else {
		host = fmt.Sprintf("%s:%d", subDomain, subPort)
	}

	// Ensure paths
	if !strings.HasPrefix(subPath, "/") {
		subPath = "/" + subPath
	}
	if !strings.HasSuffix(subPath, "/") {
		subPath = subPath + "/"
	}
	if !strings.HasPrefix(subJsonPath, "/") {
		subJsonPath = "/" + subJsonPath
	}
	if !strings.HasSuffix(subJsonPath, "/") {
		subJsonPath = subJsonPath + "/"
	}

	subURL := fmt.Sprintf("%s://%s%s%s", scheme, host, subPath, clientEntity.SubID)
	subJsonURL := fmt.Sprintf("%s://%s%s%s", scheme, host, subJsonPath, clientEntity.SubID)
	if !subJsonEnable {
		subJsonURL = ""
	}
	return subURL, subJsonURL, nil
}

// sendClientSubLinks sends the subscription links for the client to the chat.
func (t *Tgbot) sendClientSubLinks(chatId int64, email string, messageID ...int) {
	subURL, subJsonURL, err := t.buildSubscriptionURLs(email)
	if err != nil {
		logger.Warningf("Error building subscription URLs for client %s: %v", email, err)
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.wentWrong")+"\r\n"+err.Error())
		return
	}
	msg := "Subscription URL:\r\n<code>" + subURL + "</code>"
	if subJsonURL != "" {
		msg += "\r\n\r\nJSON URL:\r\n<code>" + subJsonURL + "</code>"
	}
	inlineKeyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("subscription.individualLinks")).WithCallbackData(t.encodeQuery("client_individual_links "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("qrCode")).WithCallbackData(t.encodeQuery("client_qr_links "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("get_client_info "+email)),
		),
	)
	if len(messageID) > 0 {
		err := t.editMessageTgBot(chatId, messageID[0], msg, inlineKeyboard)
		if err != nil {
			t.SendMsgToTgbot(chatId, msg, inlineKeyboard)
		}
	} else {
		t.SendMsgToTgbot(chatId, msg, inlineKeyboard)
	}
}

// sendClientIndividualLinks fetches the subscription content (individual links) and sends it to the user
func (t *Tgbot) sendClientIndividualLinks(chatId int64, email string, messageID ...int) {
	// Build the HTML sub page URL; we'll call it with header Accept to get raw content
	subURL, _, err := t.buildSubscriptionURLs(email)
	if err != nil {
		logger.Warningf("Error building subscription URLs for client %s: %v", email, err)
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.wentWrong")+"\r\n"+err.Error())
		return
	}

	// Try to fetch raw subscription links. Prefer plain text response.
	req, err := http.NewRequest("GET", subURL, nil)
	if err != nil {
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation")+"\r\n"+err.Error())
		return
	}
	// Set User-Agent as browser to pass encryption check if enabled
	// The controller allows browser User-Agent when encryption is enabled (checks Accept header for text/html)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	// Set Accept to include text/html to pass encryption check (controller checks for text/html in Accept)
	// But prioritize text/plain - controller will return plain text if html=1 is not in query and Accept has text/plain preference
	req.Header.Set("Accept", "text/plain, text/html;q=0.8, */*;q=0.1")

	// Use optimized client with connection pooling
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := optimizedHTTPClient.Do(req)
	if err != nil {
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation")+"\r\n"+err.Error())
		return
	}
	defer resp.Body.Close()

	// Check HTTP status code
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		errorMsg := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
		logger.Warningf("Failed to fetch subscription links for client %s: %s", email, errorMsg)
		if resp.StatusCode == http.StatusForbidden {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation")+"\r\n"+t.I18nBot("tgbot.messages.subscriptionForbidden"))
		} else {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation")+"\r\n"+errorMsg)
		}
		return
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation")+"\r\n"+err.Error())
		return
	}

	// If service is configured to encode (Base64), decode it
	encoded, _ := t.settingService.GetSubEncrypt()
	var content string
	if encoded {
		decoded, err := base64.StdEncoding.DecodeString(string(bodyBytes))
		if err != nil {
			// fallback to raw text
			content = string(bodyBytes)
		} else {
			content = string(decoded)
		}
	} else {
		content = string(bodyBytes)
	}

	// Check if content is HTML (contains HTML tags)
	isHTML := strings.Contains(strings.ToLower(content), "<html") || 
	          strings.Contains(strings.ToLower(content), "<!doctype") ||
	          strings.Contains(strings.ToLower(content), "<template") ||
	          strings.Contains(strings.ToLower(content), "<script")
	
	var cleaned []string
	if isHTML {
		// Parse HTML to extract subscription links
		// Look for links that start with vmess://, vless://, trojan://, ss://, or https://
		linkPattern := regexp.MustCompile(`(vmess://[^\s"<>]+|vless://[^\s"<>]+|trojan://[^\s"<>]+|ss://[^\s"<>]+|https?://[^\s"<>]+)`)
		matches := linkPattern.FindAllString(content, -1)
		for _, match := range matches {
			// Clean up HTML entities and trim
			match = strings.TrimSpace(match)
			if match != "" && !strings.Contains(match, "<") && !strings.Contains(match, ">") {
				cleaned = append(cleaned, match)
			}
		}
	} else {
		// Normalize line endings and trim
		lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
		for _, l := range lines {
			l = strings.TrimSpace(l)
			// Filter out HTML tags and non-link content
			if l != "" && !strings.HasPrefix(strings.ToLower(l), "<") && 
			   !strings.Contains(l, "<template") && !strings.Contains(l, "<a-checkbox") &&
			   !strings.Contains(l, "<script") && !strings.Contains(l, "</script>") {
				cleaned = append(cleaned, l)
			}
		}
	}
	
	if len(cleaned) == 0 {
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.noResult"))
		return
	}

	// Send in chunks to respect message length; use monospace formatting
	const maxPerMessage = 50
	for i := 0; i < len(cleaned); i += maxPerMessage {
		j := i + maxPerMessage
		if j > len(cleaned) {
			j = len(cleaned)
		}
		chunk := cleaned[i:j]
		msg := t.I18nBot("subscription.individualLinks") + ":\r\n"
		for _, link := range chunk {
			// Escape HTML special characters and wrap each link in <code>
			link = strings.ReplaceAll(link, "&", "&amp;")
			link = strings.ReplaceAll(link, "<", "&lt;")
			link = strings.ReplaceAll(link, ">", "&gt;")
			msg += "<code>" + link + "</code>\r\n"
		}
		// Add back button to the last message
		var inlineKeyboard *telego.InlineKeyboardMarkup
		if j >= len(cleaned) {
			inlineKeyboard = tu.InlineKeyboard(
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("get_client_info "+email)),
				),
			)
		}
		// Edit first message if messageID provided, otherwise send new
		if i == 0 && len(messageID) > 0 {
			err := t.editMessageTgBot(chatId, messageID[0], msg, inlineKeyboard)
			if err != nil {
				t.SendMsgToTgbot(chatId, msg, inlineKeyboard)
			}
		} else {
			t.SendMsgToTgbot(chatId, msg, inlineKeyboard)
		}
	}
}

// sendClientQRLinks generates QR images for subscription URL, JSON URL, and a few individual links, then sends them
func (t *Tgbot) sendClientQRLinks(chatId int64, email string, messageID ...int) {
	subURL, subJsonURL, err := t.buildSubscriptionURLs(email)
	if err != nil {
		logger.Warningf("Error building subscription URLs for client %s: %v", email, err)
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.wentWrong")+"\r\n"+err.Error())
		return
	}

	// Helper to create QR PNG bytes from content
	createQR := func(content string, size int) ([]byte, error) {
		if size <= 0 {
			size = 256
		}
		return qrcode.Encode(content, qrcode.Medium, size)
	}

	// Inform user - edit message if messageID provided
	qrMsg := "QRCode" + ":"
	if len(messageID) > 0 {
		err := t.editMessageTgBot(chatId, messageID[0], qrMsg, nil)
		if err != nil {
			t.SendMsgToTgbot(chatId, qrMsg)
		}
	} else {
		t.SendMsgToTgbot(chatId, qrMsg)
	}

	// Send sub URL QR (filename: sub.png)
	if png, err := createQR(subURL, 320); err == nil {
		document := tu.Document(
			tu.ID(chatId),
			tu.FileFromBytes(png, "sub.png"),
		)
		_, _ = bot.SendDocument(context.Background(), document)
	} else {
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation")+"\r\n"+err.Error())
	}

	// Send JSON URL QR (filename: subjson.png) when available
	if subJsonURL != "" {
		if png, err := createQR(subJsonURL, 320); err == nil {
			document := tu.Document(
				tu.ID(chatId),
				tu.FileFromBytes(png, "subjson.png"),
			)
			_, _ = bot.SendDocument(context.Background(), document)
		} else {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation")+"\r\n"+err.Error())
		}
	}

	// Also generate a few individual links' QRs (first up to 5)
	subPageURL := subURL
	req, err := http.NewRequest("GET", subPageURL, nil)
	if err == nil {
		req.Header.Set("Accept", "text/plain, */*;q=0.1")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req = req.WithContext(ctx)
		if resp, err := optimizedHTTPClient.Do(req); err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			encoded, _ := t.settingService.GetSubEncrypt()
			var content string
			if encoded {
				if dec, err := base64.StdEncoding.DecodeString(string(body)); err == nil {
					content = string(dec)
				} else {
					content = string(body)
				}
			} else {
				content = string(body)
			}
			lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
			var cleaned []string
			for _, l := range lines {
				l = strings.TrimSpace(l)
				if l != "" {
					cleaned = append(cleaned, l)
				}
			}
			if len(cleaned) > 0 {
				max := min(len(cleaned), 5)
				for i := range max {
					if png, err := createQR(cleaned[i], 320); err == nil {
						// Use the email as filename for individual link QR
						filename := email + ".png"
						document := tu.Document(
							tu.ID(chatId),
							tu.FileFromBytes(png, filename),
						)
						_, _ = bot.SendDocument(context.Background(), document)
						// Reduced delay for better performance
						if i < max-1 { // Only delay between documents, not after the last one
							time.Sleep(50 * time.Millisecond)
						}
					}
				}
			}
		}
	}
	
	// Add back button after sending all QR codes
	backKeyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("get_client_info "+email)),
		),
	)
	t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.successfulOperation"), backKeyboard)
}

// SendMsgToTgbotAdmins sends a message to all admin Telegram chats.
// If no replyMarkup is provided, automatically adds a delete button.
func (t *Tgbot) SendMsgToTgbotAdmins(msg string, replyMarkup ...telego.ReplyMarkup) {
	if len(replyMarkup) > 0 {
		for _, adminId := range adminIds {
			t.SendMsgToTgbot(adminId, msg, replyMarkup[0])
		}
	} else {
		// Automatically add delete button to informational messages
		for _, adminId := range adminIds {
			keyboard := t.createBackAndDeleteKeyboard(adminId, 0)
			sentMsg, err := t.sendMessageWithKeyboard(adminId, msg, keyboard)
			if err == nil && sentMsg != nil {
				// Update keyboard with actual message ID
				keyboard = t.createBackAndDeleteKeyboard(adminId, sentMsg.MessageID)
				t.editMessageTgBot(adminId, sentMsg.MessageID, msg, keyboard)
			}
		}
	}
}

// NotifyClientCreated sends a notification when a client is created.
// This is called from ClientService.AddClient and sends notifications to admins only.
func (t *Tgbot) NotifyClientCreated(client *model.ClientEntity) {
	if !t.IsRunning() {
		return
	}

	msg := t.I18nBot("tgbot.messages.clientCreated")
	msg += t.I18nBot("tgbot.messages.email", "Email=="+client.Email)
	msg += t.I18nBot("tgbot.messages.status", "Status=="+client.Status)
	msg += t.I18nBot("tgbot.messages.clientEnabled", "Enabled=="+fmt.Sprintf("%v", client.Enable))
	msg += t.I18nBot("tgbot.messages.trafficLimit", "Limit=="+t.formatTrafficLimitLocalized(client.TotalGB))
	msg += t.I18nBot("tgbot.messages.expiryTime", "Time=="+t.formatExpiryTimeLocalized(client.ExpiryTime))
	msg += t.I18nBot("tgbot.messages.time", "Time=="+time.Now().Format("2006-01-02 15:04:05"))

	if client.Comment != "" {
		msg += t.I18nBot("tgbot.messages.comment", "Comment=="+client.Comment)
	}

	// Send only to admins, not to the user who created the client
	t.SendMsgToTgbotAdmins(msg)
}

// NotifyClientUpdated sends a notification when a client is updated.
func (t *Tgbot) NotifyClientUpdated(client *model.ClientEntity, oldClient *model.ClientEntity) {
	if !t.IsRunning() {
		return
	}

	msg := t.I18nBot("tgbot.messages.clientUpdated")
	msg += t.I18nBot("tgbot.messages.email", "Email=="+client.Email)
	msg += t.I18nBot("tgbot.messages.status", "Status=="+client.Status)
	msg += t.I18nBot("tgbot.messages.clientEnabled", "Enabled=="+fmt.Sprintf("%v", client.Enable))
	msg += t.I18nBot("tgbot.messages.trafficLimit", "Limit=="+t.formatTrafficLimitLocalized(client.TotalGB))
	msg += t.I18nBot("tgbot.messages.expiryTime", "Time=="+t.formatExpiryTimeLocalized(client.ExpiryTime))
	msg += t.I18nBot("tgbot.messages.time", "Time=="+time.Now().Format("2006-01-02 15:04:05"))

	// Add traffic information
	totalTraffic := client.Up + client.Down
	msg += t.I18nBot("tgbot.messages.trafficInfo",
		"Upload=="+common.FormatTraffic(client.Up),
		"Download=="+common.FormatTraffic(client.Down),
		"Total=="+common.FormatTraffic(totalTraffic))

	// Add HWID information
	if client.HWIDEnabled {
		hwidCount := 0
		if client.HWIDs != nil {
			hwidCount = len(client.HWIDs)
		}
		maxHwidText := "∞"
		if client.MaxHWID > 0 {
			maxHwidText = fmt.Sprintf("%d", client.MaxHWID)
		}
		msg += t.I18nBot("tgbot.messages.hwidEnabled", "Count=="+fmt.Sprintf("%d", hwidCount), "Max=="+maxHwidText)
	} else {
		msg += t.I18nBot("tgbot.messages.hwidDisabled")
	}

	if oldClient != nil {
		changes := []string{}
		if oldClient.Email != client.Email {
			changes = append(changes, t.I18nBot("tgbot.messages.emailChanged", "Old=="+oldClient.Email, "New=="+client.Email))
		}
		if oldClient.Enable != client.Enable {
			changes = append(changes, t.I18nBot("tgbot.messages.enabledChanged", "Old=="+fmt.Sprintf("%v", oldClient.Enable), "New=="+fmt.Sprintf("%v", client.Enable)))
		}
		if oldClient.TotalGB != client.TotalGB {
			changes = append(changes, t.I18nBot("tgbot.messages.trafficLimitChanged", "Old=="+t.formatTrafficLimitLocalized(oldClient.TotalGB), "New=="+t.formatTrafficLimitLocalized(client.TotalGB)))
		}
		if oldClient.UUID != client.UUID {
			changes = append(changes, t.I18nBot("tgbot.messages.uuidChanged", "Old=="+oldClient.UUID, "New=="+client.UUID))
		}
		if oldClient.Password != client.Password {
			// Don't show full password, just indicate it changed
			changes = append(changes, t.I18nBot("tgbot.messages.passwordChanged"))
		}
		if oldClient.ExpiryTime != client.ExpiryTime {
			changes = append(changes, t.I18nBot("tgbot.messages.expiryTimeChanged", "Old=="+t.formatExpiryTimeLocalized(oldClient.ExpiryTime), "New=="+t.formatExpiryTimeLocalized(client.ExpiryTime)))
		}
		if oldClient.Status != client.Status {
			changes = append(changes, t.I18nBot("tgbot.messages.statusChanged", "Old=="+oldClient.Status, "New=="+client.Status))
		}
		
		// Check traffic changes
		oldTotalTraffic := oldClient.Up + oldClient.Down
		if oldClient.Up != client.Up || oldClient.Down != client.Down {
			changes = append(changes, t.I18nBot("tgbot.messages.trafficChanged",
				"OldUpload=="+common.FormatTraffic(oldClient.Up),
				"NewUpload=="+common.FormatTraffic(client.Up),
				"OldDownload=="+common.FormatTraffic(oldClient.Down),
				"NewDownload=="+common.FormatTraffic(client.Down),
				"OldTotal=="+common.FormatTraffic(oldTotalTraffic),
				"NewTotal=="+common.FormatTraffic(totalTraffic)))
		}
		
		// Check HWID changes
		if oldClient.HWIDEnabled != client.HWIDEnabled {
			oldHwidText := t.I18nBot("tgbot.messages.hwidDisabled")
			if oldClient.HWIDEnabled {
				oldMaxHwidText := "∞"
				if oldClient.MaxHWID > 0 {
					oldMaxHwidText = fmt.Sprintf("%d", oldClient.MaxHWID)
				}
				oldHwidText = t.I18nBot("tgbot.messages.hwidEnabled", "Count==0", "Max=="+oldMaxHwidText)
			}
			newHwidText := t.I18nBot("tgbot.messages.hwidDisabled")
			if client.HWIDEnabled {
				newMaxHwidText := "∞"
				if client.MaxHWID > 0 {
					newMaxHwidText = fmt.Sprintf("%d", client.MaxHWID)
				}
				hwidCount := 0
				if client.HWIDs != nil {
					hwidCount = len(client.HWIDs)
				}
				newHwidText = t.I18nBot("tgbot.messages.hwidEnabled", "Count=="+fmt.Sprintf("%d", hwidCount), "Max=="+newMaxHwidText)
			}
			changes = append(changes, t.I18nBot("tgbot.messages.hwidChanged", "Old=="+oldHwidText, "New=="+newHwidText))
		} else if client.HWIDEnabled && oldClient.MaxHWID != client.MaxHWID {
			oldMaxHwidText := "∞"
			if oldClient.MaxHWID > 0 {
				oldMaxHwidText = fmt.Sprintf("%d", oldClient.MaxHWID)
			}
			newMaxHwidText := "∞"
			if client.MaxHWID > 0 {
				newMaxHwidText = fmt.Sprintf("%d", client.MaxHWID)
			}
			changes = append(changes, t.I18nBot("tgbot.messages.hwidLimitChanged", "Old=="+oldMaxHwidText, "New=="+newMaxHwidText))
		}
		
		if len(changes) > 0 {
			msg += "\n\n" + t.I18nBot("tgbot.messages.changes") + "\n" + strings.Join(changes, "\n")
		}
	}

	if client.Comment != "" {
		msg += t.I18nBot("tgbot.messages.comment", "Comment=="+client.Comment)
	}

	t.SendMsgToTgbotAdmins(msg)
}

// NotifyClientDeleted sends a notification when a client is deleted.
func (t *Tgbot) NotifyClientDeleted(client *model.ClientEntity) {
	if !t.IsRunning() {
		return
	}

	msg := t.I18nBot("tgbot.messages.clientDeleted")
	msg += t.I18nBot("tgbot.messages.email", "Email=="+client.Email)
	msg += t.I18nBot("tgbot.messages.time", "Time=="+time.Now().Format("2006-01-02 15:04:05"))

	if client.Comment != "" {
		msg += t.I18nBot("tgbot.messages.comment", "Comment=="+client.Comment)
	}

	t.SendMsgToTgbotAdmins(msg)
}

// NotifyClientDisabled sends a notification when a client is disabled.
func (t *Tgbot) NotifyClientDisabled(client *model.ClientEntity) {
	if !t.IsRunning() {
		return
	}

	msg := t.I18nBot("tgbot.messages.clientDisabled")
	msg += t.I18nBot("tgbot.messages.email", "Email=="+client.Email)
	msg += t.I18nBot("tgbot.messages.status", "Status=="+client.Status)
	msg += t.I18nBot("tgbot.messages.time", "Time=="+time.Now().Format("2006-01-02 15:04:05"))

	if client.Comment != "" {
		msg += t.I18nBot("tgbot.messages.comment", "Comment=="+client.Comment)
	}

	t.SendMsgToTgbotAdmins(msg)
}

// NotifyClientFirstConnection sends a notification when a client connects for the first time.
func (t *Tgbot) NotifyClientFirstConnection(client *model.ClientEntity) {
	if !t.IsRunning() {
		return
	}

	msg := t.I18nBot("tgbot.messages.clientFirstConnection")
	msg += t.I18nBot("tgbot.messages.email", "Email=="+client.Email)
	msg += t.I18nBot("tgbot.messages.time", "Time=="+time.Now().Format("2006-01-02 15:04:05"))

	if client.Comment != "" {
		msg += t.I18nBot("tgbot.messages.comment", "Comment=="+client.Comment)
	}

	t.SendMsgToTgbotAdmins(msg)
}

// NotifyInboundCreated sends a notification when an inbound is created.
func (t *Tgbot) NotifyInboundCreated(inbound *model.Inbound) {
	if !t.IsRunning() {
		return
	}

	msg := fmt.Sprintf("✅ <b>Инбаунд создан</b>\n\n"+
		"<b>Название:</b> %s\n"+
		"<b>Протокол:</b> %s\n"+
		"<b>Порт:</b> %d\n"+
		"<b>Включен:</b> %v\n"+
		"<b>Время:</b> %s",
		inbound.Remark,
		inbound.Protocol,
		inbound.Port,
		inbound.Enable,
		time.Now().Format("2006-01-02 15:04:05"))

	if inbound.Listen != "" && inbound.Listen != "0.0.0.0" {
		msg += fmt.Sprintf("\n<b>Listen:</b> %s", inbound.Listen)
	}

	t.SendMsgToTgbotAdmins(msg)
}

// NotifyInboundUpdated sends a notification when an inbound is updated.
func (t *Tgbot) NotifyInboundUpdated(inbound *model.Inbound, oldInbound *model.Inbound) {
	if !t.IsRunning() {
		return
	}

	msg := fmt.Sprintf("🔄 <b>Инбаунд изменен</b>\n\n"+
		"<b>Название:</b> %s\n"+
		"<b>Протокол:</b> %s\n"+
		"<b>Порт:</b> %d\n"+
		"<b>Включен:</b> %v\n"+
		"<b>Время:</b> %s",
		inbound.Remark,
		inbound.Protocol,
		inbound.Port,
		inbound.Enable,
		time.Now().Format("2006-01-02 15:04:05"))

	if oldInbound != nil {
		changes := []string{}
		if oldInbound.Remark != inbound.Remark {
			changes = append(changes, fmt.Sprintf("Название: %s → %s", oldInbound.Remark, inbound.Remark))
		}
		if oldInbound.Port != inbound.Port {
			changes = append(changes, fmt.Sprintf("Порт: %d → %d", oldInbound.Port, inbound.Port))
		}
		if oldInbound.Protocol != inbound.Protocol {
			changes = append(changes, fmt.Sprintf("Протокол: %s → %s", oldInbound.Protocol, inbound.Protocol))
		}
		if oldInbound.Enable != inbound.Enable {
			changes = append(changes, fmt.Sprintf("Включен: %v → %v", oldInbound.Enable, inbound.Enable))
		}
		if len(changes) > 0 {
			msg += "\n\n<b>Изменения:</b>\n" + strings.Join(changes, "\n")
		}
	}

	if inbound.Listen != "" && inbound.Listen != "0.0.0.0" {
		msg += fmt.Sprintf("\n<b>Listen:</b> %s", inbound.Listen)
	}

	t.SendMsgToTgbotAdmins(msg)
}

// NotifyInboundDeleted sends a notification when an inbound is deleted.
func (t *Tgbot) NotifyInboundDeleted(inbound *model.Inbound) {
	if !t.IsRunning() {
		return
	}

	msg := fmt.Sprintf("❌ <b>Инбаунд удален</b>\n\n"+
		"<b>Название:</b> %s\n"+
		"<b>Протокол:</b> %s\n"+
		"<b>Порт:</b> %d\n"+
		"<b>Время:</b> %s",
		inbound.Remark,
		inbound.Protocol,
		inbound.Port,
		time.Now().Format("2006-01-02 15:04:05"))

	if inbound.Listen != "" && inbound.Listen != "0.0.0.0" {
		msg += fmt.Sprintf("\n<b>Listen:</b> %s", inbound.Listen)
	}

	t.SendMsgToTgbotAdmins(msg)
}

// NotifyGroupChanged sends a notification when a group's clients are enabled/disabled.
func (t *Tgbot) NotifyGroupChanged(groupName string, enable bool, clients []*model.ClientEntity) {
	if !t.IsRunning() {
		return
	}

	if len(clients) == 0 {
		return
	}

	action := "отключена"
	emoji := "⛔"
	if enable {
		action = "включена"
		emoji = "✅"
	}

	msg := fmt.Sprintf("%s <b>Группа изменена</b>\n\n"+
		"<b>Название:</b> %s\n"+
		"<b>Действие:</b> %s\n"+
		"<b>Количество клиентов:</b> %d\n"+
		"<b>Время:</b> %s",
		emoji, groupName, action, len(clients),
		time.Now().Format("2006-01-02 15:04:05"))

	// Add list of affected clients (limit to 10 to avoid too long messages)
	clientList := ""
	maxClients := 10
	if len(clients) > maxClients {
		for i := 0; i < maxClients; i++ {
			if clients[i].Comment != "" {
				clientList += fmt.Sprintf("• %s (%s)\n", clients[i].Email, clients[i].Comment)
			} else {
				clientList += fmt.Sprintf("• %s\n", clients[i].Email)
			}
		}
		clientList += fmt.Sprintf("... и еще %d клиентов\n", len(clients)-maxClients)
	} else {
		for _, client := range clients {
			if client.Comment != "" {
				clientList += fmt.Sprintf("• %s (%s)\n", client.Email, client.Comment)
			} else {
				clientList += fmt.Sprintf("• %s\n", client.Email)
			}
		}
	}

	if clientList != "" {
		msg += "\n\n<b>Клиенты:</b>\n" + clientList
	}

	t.SendMsgToTgbotAdmins(msg)
}

// Helper functions for formatting
func formatTrafficLimit(totalGB float64) string {
	if totalGB == 0 {
		return "Безлимит"
	}
	// Format with 2 decimal places for small values, integer for large values
	if totalGB < 1 {
		return fmt.Sprintf("%.2f GB", totalGB)
	}
	if totalGB == float64(int64(totalGB)) {
		return fmt.Sprintf("%d GB", int64(totalGB))
	}
	return fmt.Sprintf("%.2f GB", totalGB)
}

func formatExpiryTime(expiryTime int64) string {
	if expiryTime == 0 {
		return "Без срока"
	}
	t := time.Unix(expiryTime/1000, 0)
	return t.Format("2006-01-02 15:04:05")
}

// formatTrafficLimitLocalized formats traffic limit with localization
func (t *Tgbot) formatTrafficLimitLocalized(totalGB float64) string {
	if totalGB == 0 {
		return t.I18nBot("tgbot.messages.unlimitedTraffic")
	}
	// Format with 2 decimal places for small values, integer for large values
	if totalGB < 1 {
		return fmt.Sprintf("%.2f GB", totalGB)
	}
	if totalGB == float64(int64(totalGB)) {
		return fmt.Sprintf("%d GB", int64(totalGB))
	}
	return fmt.Sprintf("%.2f GB", totalGB)
}

// formatExpiryTimeLocalized formats expiry time with localization
func (t *Tgbot) formatExpiryTimeLocalized(expiryTime int64) string {
	if expiryTime == 0 {
		return t.I18nBot("tgbot.messages.noExpiry")
	}
	expTime := time.Unix(expiryTime/1000, 0)
	return expTime.Format("2006-01-02 15:04:05")
}

// SendReport sends a periodic report to admin chats.
func (t *Tgbot) SendReport() {
	runTime, err := t.settingService.GetTgbotRuntime()
	if err == nil && len(runTime) > 0 {
		msg := ""
		msg += t.I18nBot("tgbot.messages.report", "RunTime=="+runTime)
		msg += t.I18nBot("tgbot.messages.datetime", "DateTime=="+time.Now().Format("2006-01-02 15:04:05"))
		t.SendMsgToTgbotAdmins(msg)
	}

	info := t.sendServerUsage()
	t.SendMsgToTgbotAdmins(info)

	t.sendExhaustedToAdmins()
	t.notifyExhausted()

	backupEnable, err := t.settingService.GetTgBotBackup()
	if err == nil && backupEnable {
		t.SendBackupToAdmins()
	}
}

// SendBackupToAdmins sends a database backup to admin chats.
func (t *Tgbot) SendBackupToAdmins() {
	if !t.IsRunning() {
		return
	}
	for _, adminId := range adminIds {
		t.sendBackup(int64(adminId))
	}
}

// sendExhaustedToAdmins sends notifications about exhausted clients to admins.
func (t *Tgbot) sendExhaustedToAdmins() {
	if !t.IsRunning() {
		return
	}
	for _, adminId := range adminIds {
		t.getExhausted(int64(adminId))
	}
}

// getServerUsage retrieves and formats server usage information.
func (t *Tgbot) getServerUsage(chatId int64, messageID ...int) string {
	info := t.prepareServerUsageInfo()

	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("usage_refresh")),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("back_to_main")),
		),
	)

	if len(messageID) > 0 {
		t.editMessageTgBot(chatId, messageID[0], info, keyboard)
	} else {
		t.SendMsgToTgbot(chatId, info, keyboard)
	}

	return info
}

// Send server usage without an inline keyboard
func (t *Tgbot) sendServerUsage() string {
	info := t.prepareServerUsageInfo()
	return info
}

// prepareServerUsageInfo prepares the server usage information string.
func (t *Tgbot) prepareServerUsageInfo() string {
	// Check if we have cached data first
	if cachedStats, found := t.getCachedServerStats(); found {
		return cachedStats
	}

	info, ipv4, ipv6 := "", "", ""

	// get latest status of server with caching
	if cachedStatus, found := t.getCachedStatus(); found {
		t.lastStatus = cachedStatus
	} else {
		t.lastStatus = t.serverService.GetStatus(t.lastStatus)
		t.setCachedStatus(t.lastStatus)
	}
	onlines := p.GetOnlineClients()

	info += t.I18nBot("tgbot.messages.hostname", "Hostname=="+hostname)
	info += t.I18nBot("tgbot.messages.version", "Version=="+config.GetVersion())
	info += t.I18nBot("tgbot.messages.xrayVersion", "XrayVersion=="+fmt.Sprint(t.lastStatus.Xray.Version))

	// get ip address
	netInterfaces, err := net.Interfaces()
	if err != nil {
		logger.Error("net.Interfaces failed, err: ", err.Error())
		info += t.I18nBot("tgbot.messages.ip", "IP=="+t.I18nBot("tgbot.unknown"))
		info += "\r\n"
	} else {
		for i := 0; i < len(netInterfaces); i++ {
			if (netInterfaces[i].Flags & net.FlagUp) != 0 {
				addrs, _ := netInterfaces[i].Addrs()

				for _, address := range addrs {
					if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
						if ipnet.IP.To4() != nil {
							ipv4 += ipnet.IP.String() + " "
						} else if ipnet.IP.To16() != nil && !ipnet.IP.IsLinkLocalUnicast() {
							ipv6 += ipnet.IP.String() + " "
						}
					}
				}
			}
		}

		info += t.I18nBot("tgbot.messages.ipv4", "IPv4=="+ipv4)
		info += t.I18nBot("tgbot.messages.ipv6", "IPv6=="+ipv6)
	}

	info += t.I18nBot("tgbot.messages.serverUpTime", "UpTime=="+strconv.FormatUint(t.lastStatus.Uptime/86400, 10), "Unit=="+t.I18nBot("tgbot.days"))
	info += t.I18nBot("tgbot.messages.serverLoad", "Load1=="+strconv.FormatFloat(t.lastStatus.Loads[0], 'f', 2, 64), "Load2=="+strconv.FormatFloat(t.lastStatus.Loads[1], 'f', 2, 64), "Load3=="+strconv.FormatFloat(t.lastStatus.Loads[2], 'f', 2, 64))
	info += t.I18nBot("tgbot.messages.serverMemory", "Current=="+common.FormatTraffic(int64(t.lastStatus.Mem.Current)), "Total=="+common.FormatTraffic(int64(t.lastStatus.Mem.Total)))
	info += t.I18nBot("tgbot.messages.onlinesCount", "Count=="+fmt.Sprint(len(onlines)))
	info += t.I18nBot("tgbot.messages.tcpCount", "Count=="+strconv.Itoa(t.lastStatus.TcpCount))
	info += t.I18nBot("tgbot.messages.udpCount", "Count=="+strconv.Itoa(t.lastStatus.UdpCount))
	info += t.I18nBot("tgbot.messages.traffic", "Total=="+common.FormatTraffic(int64(t.lastStatus.NetTraffic.Sent+t.lastStatus.NetTraffic.Recv)), "Upload=="+common.FormatTraffic(int64(t.lastStatus.NetTraffic.Sent)), "Download=="+common.FormatTraffic(int64(t.lastStatus.NetTraffic.Recv)))
	info += t.I18nBot("tgbot.messages.xrayStatus", "State=="+fmt.Sprint(t.lastStatus.Xray.State))

	// Cache the complete server stats
	t.setCachedServerStats(info)

	return info
}

// UserLoginNotify sends a notification about user login attempts to admins.
func (t *Tgbot) UserLoginNotify(username string, password string, ip string, time string, status LoginStatus) {
	if !t.IsRunning() {
		return
	}

	if username == "" || ip == "" || time == "" {
		logger.Warning("UserLoginNotify failed, invalid info!")
		return
	}

	loginNotifyEnabled, err := t.settingService.GetTgBotLoginNotify()
	if err != nil || !loginNotifyEnabled {
		return
	}

	msg := ""
	switch status {
	case LoginSuccess:
		msg += t.I18nBot("tgbot.messages.loginSuccess")
		msg += t.I18nBot("tgbot.messages.hostname", "Hostname=="+hostname)
	case LoginFail:
		msg += t.I18nBot("tgbot.messages.loginFailed")
		msg += t.I18nBot("tgbot.messages.hostname", "Hostname=="+hostname)
		msg += t.I18nBot("tgbot.messages.password", "Password=="+password)
	}
	msg += t.I18nBot("tgbot.messages.username", "Username=="+username)
	msg += t.I18nBot("tgbot.messages.ip", "IP=="+ip)
	msg += t.I18nBot("tgbot.messages.time", "Time=="+time)
	t.SendMsgToTgbotAdmins(msg)
}

// getInboundUsages retrieves and formats inbound usage information.
func (t *Tgbot) getInboundUsages() string {
	info := ""
	// get traffic
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("GetAllInbounds run failed:", err)
		info += t.I18nBot("tgbot.answers.getInboundsFailed")
	} else {
		// NOTE:If there no any sessions here,need to notify here
		// TODO:Sub-node push, automatic conversion format
		for _, inbound := range inbounds {
			info += t.I18nBot("tgbot.messages.inbound", "Remark=="+inbound.Remark)
			info += t.I18nBot("tgbot.messages.port", "Port=="+strconv.Itoa(inbound.Port))
			info += t.I18nBot("tgbot.messages.traffic", "Total=="+common.FormatTraffic((inbound.Up+inbound.Down)), "Upload=="+common.FormatTraffic(inbound.Up), "Download=="+common.FormatTraffic(inbound.Down))

			if inbound.ExpiryTime == 0 {
				info += t.I18nBot("tgbot.messages.expire", "Time=="+t.I18nBot("tgbot.unlimited"))
			} else {
				info += t.I18nBot("tgbot.messages.expire", "Time=="+time.Unix((inbound.ExpiryTime/1000), 0).Format("2006-01-02 15:04:05"))
			}
			info += "\r\n"
		}
	}
	return info
}

// getInbounds creates an inline keyboard with all inbounds.
func (t *Tgbot) getInbounds() (*telego.InlineKeyboardMarkup, error) {
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("GetAllInbounds run failed:", err)
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}

	if len(inbounds) == 0 {
		logger.Warning("No inbounds found")
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}

	var buttons []telego.InlineKeyboardButton
	for _, inbound := range inbounds {
		status := "❌"
		if inbound.Enable {
			status = "✅"
		}
		// Format: Remark (ID: X, Port: Y) - Status
		buttonText := fmt.Sprintf("%s (ID: %d, Port: %d) - %s", inbound.Remark, inbound.Id, inbound.Port, status)
		callbackData := t.encodeQuery(fmt.Sprintf("%s %d", "get_clients", inbound.Id))
		buttons = append(buttons, tu.InlineKeyboardButton(buttonText).WithCallbackData(callbackData))
	}

	cols := 1
	if len(buttons) >= 6 {
		cols = 2
	}

	// Add back button to main menu
	backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("back_to_main"))
	buttons = append(buttons, backButton)

	keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols, buttons...))
	return keyboard, nil
}

// getInboundsFor builds an inline keyboard of inbounds for a custom next action.
func (t *Tgbot) getInboundsFor(nextAction string) (*telego.InlineKeyboardMarkup, error) {
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("GetAllInbounds run failed:", err)
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}

	if len(inbounds) == 0 {
		logger.Warning("No inbounds found")
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}

	var buttons []telego.InlineKeyboardButton
	for _, inbound := range inbounds {
		status := "❌"
		if inbound.Enable {
			status = "✅"
		}
		// Format: Remark (ID: X, Port: Y) - Status
		buttonText := fmt.Sprintf("%s (ID: %d, Port: %d) - %s", inbound.Remark, inbound.Id, inbound.Port, status)
		callbackData := t.encodeQuery(fmt.Sprintf("%s %d", nextAction, inbound.Id))
		buttons = append(buttons, tu.InlineKeyboardButton(buttonText).WithCallbackData(callbackData))
	}

	cols := 1
	if len(buttons) >= 6 {
		cols = 2
	}

	// Add back button to main menu
	backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("back_to_main"))
	buttons = append(buttons, backButton)

	keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols, buttons...))
	return keyboard, nil
}

// getInboundClientsFor lists clients of an inbound with a specific action prefix to be appended with email
func (t *Tgbot) getInboundClientsFor(inboundID int, action string) (*telego.InlineKeyboardMarkup, error) {
	inbound, err := t.inboundService.GetInbound(inboundID)
	if err != nil {
		logger.Warning("getInboundClientsFor run failed:", err)
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}
	clients, err := t.inboundService.GetClients(inbound)
	var buttons []telego.InlineKeyboardButton

	if err != nil {
		logger.Warning("GetInboundClients run failed:", err)
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	} else {
		if len(clients) > 0 {
			for _, client := range clients {
				// Format: Email (Comment) if comment exists, otherwise just Email
				buttonText := client.Email
				if client.Comment != "" {
					buttonText = fmt.Sprintf("%s (%s)", client.Email, client.Comment)
				}
				buttons = append(buttons, tu.InlineKeyboardButton(buttonText).WithCallbackData(t.encodeQuery(action+" "+client.Email)))
			}

		} else {
			return nil, errors.New(t.I18nBot("tgbot.answers.getClientsFailed"))
		}

	}
	cols := 0
	if len(buttons) < 6 {
		cols = 3
	} else {
		cols = 2
	}
	
	// Add back button to inbounds list
	backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("get_inbounds"))
	buttons = append(buttons, backButton)
	
	keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols, buttons...))

	return keyboard, nil
}

// getInboundsAddClient creates an inline keyboard for adding clients to inbounds.
func (t *Tgbot) getInboundsAddClient() (*telego.InlineKeyboardMarkup, error) {
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("GetAllInbounds run failed:", err)
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}

	if len(inbounds) == 0 {
		logger.Warning("No inbounds found")
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}

	excludedProtocols := map[model.Protocol]bool{
		model.Tunnel:    true,
		model.Mixed:     true,
		model.WireGuard: true,
		model.HTTP:      true,
	}

	var buttons []telego.InlineKeyboardButton
	for _, inbound := range inbounds {
		if excludedProtocols[inbound.Protocol] {
			continue
		}

		status := "❌"
		if inbound.Enable {
			status = "✅"
		}
		// Format: Remark (ID: X, Port: Y) - Status
		buttonText := fmt.Sprintf("%s (ID: %d, Port: %d) - %s", inbound.Remark, inbound.Id, inbound.Port, status)
		callbackData := t.encodeQuery(fmt.Sprintf("%s %d", "add_client_to", inbound.Id))
		buttons = append(buttons, tu.InlineKeyboardButton(buttonText).WithCallbackData(callbackData))
	}

	cols := 1
	if len(buttons) >= 6 {
		cols = 2
	}

	// Add back button to main menu
	backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("back_to_main"))
	buttons = append(buttons, backButton)

	keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols, buttons...))
	return keyboard, nil
}

// getInboundClients creates an inline keyboard with clients of a specific inbound.
func (t *Tgbot) getInboundClients(id int) (*telego.InlineKeyboardMarkup, error) {
	inbound, err := t.inboundService.GetInbound(id)
	if err != nil {
		logger.Warning("getIboundClients run failed:", err)
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}
	clients, err := t.inboundService.GetClients(inbound)
	var buttons []telego.InlineKeyboardButton

	if err != nil {
		logger.Warning("GetInboundClients run failed:", err)
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	} else {
		if len(clients) > 0 {
			for _, client := range clients {
				// Format: Email (Comment) if comment exists, otherwise just Email
				buttonText := client.Email
				if client.Comment != "" {
					buttonText = fmt.Sprintf("%s (%s)", client.Email, client.Comment)
				}
				buttons = append(buttons, tu.InlineKeyboardButton(buttonText).WithCallbackData(t.encodeQuery("get_client_info "+client.Email)))
			}

		} else {
			return nil, errors.New(t.I18nBot("tgbot.answers.getClientsFailed"))
		}

	}
	cols := 0
	if len(buttons) < 6 {
		cols = 3
	} else {
		cols = 2
	}
	
	// Add back button to inbounds list
	backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("get_inbounds"))
	buttons = append(buttons, backButton)
	
	keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols, buttons...))

	return keyboard, nil
}

// clientInfoMsg formats client information message based on traffic and flags.
func (t *Tgbot) clientInfoMsg(
	traffic *xray.ClientTraffic,
	printEnabled bool,
	printOnline bool,
	printActive bool,
	printDate bool,
	printTraffic bool,
	printRefreshed bool,
) string {
	now := time.Now().Unix()
	expiryTime := ""
	flag := false
	diff := traffic.ExpiryTime/1000 - now
	if traffic.ExpiryTime == 0 {
		expiryTime = t.I18nBot("tgbot.unlimited")
	} else if diff > 172800 || !traffic.Enable {
		expiryTime = time.Unix((traffic.ExpiryTime / 1000), 0).Format("2006-01-02 15:04:05")
		if diff > 0 {
			days := diff / 86400
			hours := (diff % 86400) / 3600
			minutes := (diff % 3600) / 60
			remainingTime := ""
			if days > 0 {
				remainingTime += fmt.Sprintf("%d %s ", days, t.I18nBot("tgbot.days"))
			}
			if hours > 0 {
				remainingTime += fmt.Sprintf("%d %s ", hours, t.I18nBot("tgbot.hours"))
			}
			if minutes > 0 {
				remainingTime += fmt.Sprintf("%d %s", minutes, t.I18nBot("tgbot.minutes"))
			}
			expiryTime += fmt.Sprintf(" (%s)", remainingTime)
		}
	} else if traffic.ExpiryTime < 0 {
		expiryTime = fmt.Sprintf("%d %s", traffic.ExpiryTime/-86400000, t.I18nBot("tgbot.days"))
		flag = true
	} else {
		expiryTime = fmt.Sprintf("%d %s", diff/3600, t.I18nBot("tgbot.hours"))
		flag = true
	}

	total := ""
	if traffic.Total == 0 {
		total = t.I18nBot("tgbot.unlimited")
	} else {
		total = common.FormatTraffic((traffic.Total))
	}

	enabled := ""
	// Use traffic.Enable directly (already set from ClientEntity in new architecture)
	// This avoids calling checkIsEnabledByEmail which may fail with new architecture
	if traffic.Enable {
		enabled = t.I18nBot("tgbot.messages.yes")
	} else {
		enabled = t.I18nBot("tgbot.messages.no")
	}

	active := ""
	if traffic.Enable {
		active = t.I18nBot("tgbot.messages.yes")
	} else {
		active = t.I18nBot("tgbot.messages.no")
	}

	status := t.I18nBot("tgbot.offline")
	isOnline := false
	if p.IsRunning() {
		for _, online := range p.GetOnlineClients() {
			if online == traffic.Email {
				status = t.I18nBot("tgbot.online")
				isOnline = true
				break
			}
		}
	}

	output := ""
	output += t.I18nBot("tgbot.messages.email", "Email=="+traffic.Email)
	if printEnabled {
		output += t.I18nBot("tgbot.messages.enabled", "Enable=="+enabled)
	}
	if printOnline {
		output += t.I18nBot("tgbot.messages.online", "Status=="+status)
		if !isOnline && traffic.LastOnline > 0 {
			output += t.I18nBot("tgbot.messages.lastOnline", "Time=="+time.UnixMilli(traffic.LastOnline).Format("2006-01-02 15:04:05"))
		}
	}
	if printActive {
		output += t.I18nBot("tgbot.messages.active", "Enable=="+active)
	}
	if printDate {
		if flag {
			output += t.I18nBot("tgbot.messages.expireIn", "Time=="+expiryTime)
		} else {
			output += t.I18nBot("tgbot.messages.expire", "Time=="+expiryTime)
		}
	}
	if printTraffic {
		output += t.I18nBot("tgbot.messages.upload", "Upload=="+common.FormatTraffic(traffic.Up))
		output += t.I18nBot("tgbot.messages.download", "Download=="+common.FormatTraffic(traffic.Down))
		output += t.I18nBot("tgbot.messages.total", "UpDown=="+common.FormatTraffic((traffic.Up+traffic.Down)), "Total=="+total)
	}
	if printRefreshed {
		output += t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))
	}

	return output
}

// getClientTrafficTgBotNew gets clients by Telegram ID using new architecture
func (t *Tgbot) getClientTrafficTgBotNew(tgUserID int64) ([]*xray.ClientTraffic, error) {
	db := database.GetDB()
	
	// Find clients by TgID in ClientEntity (new architecture)
	var clientEntities []model.ClientEntity
	err := db.Where("tg_id = ?", tgUserID).Find(&clientEntities).Error
	if err != nil {
		return nil, err
	}
	
	if len(clientEntities) == 0 {
		return []*xray.ClientTraffic{}, nil
	}
	
	// Convert ClientEntity to ClientTraffic
	traffics := make([]*xray.ClientTraffic, len(clientEntities))
	for i, entity := range clientEntities {
		totalBytes := int64(entity.TotalGB * 1024 * 1024 * 1024)
		traffics[i] = &xray.ClientTraffic{
			Id:         0, // Not used in new architecture
			InboundId:  0, // Will be set if needed
			Enable:     entity.Enable,
			Email:      entity.Email,
			UUID:       entity.UUID,
			SubId:      entity.SubID,
			Up:         entity.Up,
			Down:       entity.Down,
			AllTime:    entity.AllTime,
			ExpiryTime: entity.ExpiryTime,
			Total:      totalBytes,
			Reset:      entity.Reset,
			LastOnline: entity.LastOnline,
		}
	}
	
	return traffics, nil
}

// getClientUsage retrieves and sends client usage information to the chat.
// emailOrMessageID can be: email (string) or messageID (int/int64)
func (t *Tgbot) getClientUsage(chatId int64, tgUserID int64, emailOrMessageID ...interface{}) {
	// Parse variadic arguments: can be email (string) or messageID (int)
	var email string
	var messageID int
	hasEmail := false
	hasMessageID := false
	
	for _, arg := range emailOrMessageID {
		switch v := arg.(type) {
		case string:
			email = v
			hasEmail = true
		case int:
			messageID = v
			hasMessageID = true
		case int64:
			messageID = int(v)
			hasMessageID = true
		}
	}
	
	// Try new architecture first
	traffics, err := t.getClientTrafficTgBotNew(tgUserID)
	if err != nil || len(traffics) == 0 {
		// Fallback to old method
		traffics, err = t.inboundService.GetClientTrafficTgBot(tgUserID)
		if err != nil {
			logger.Warning(err)
			msg := t.I18nBot("tgbot.wentWrong")
			if hasMessageID {
				err := t.editMessageTgBot(chatId, messageID, msg, nil)
				if err != nil {
					t.SendMsgToTgbot(chatId, msg)
				}
			} else {
				t.SendMsgToTgbot(chatId, msg)
			}
			return
		}
	}

	if len(traffics) == 0 {
		msg := t.I18nBot("tgbot.answers.askToAddUserId", "TgUserID=="+strconv.FormatInt(tgUserID, 10))
		if hasMessageID {
			err := t.editMessageTgBot(chatId, messageID, msg, nil)
			if err != nil {
				t.SendMsgToTgbot(chatId, msg)
			}
		} else {
			t.SendMsgToTgbot(chatId, msg)
		}
		return
	}

	output := ""

	if len(traffics) > 0 {
		if hasEmail {
			for _, traffic := range traffics {
				if traffic.Email == email {
					output := t.clientInfoMsg(traffic, true, true, true, true, true, true)
					if hasMessageID {
						err := t.editMessageTgBot(chatId, messageID, output, nil)
						if err != nil {
							t.SendMsgToTgbot(chatId, output)
						}
					} else {
						t.SendMsgToTgbot(chatId, output)
					}
					return
				}
			}
			msg := t.I18nBot("tgbot.noResult")
			if hasMessageID {
				err := t.editMessageTgBot(chatId, messageID, msg, nil)
				if err != nil {
					t.SendMsgToTgbot(chatId, msg)
				}
			} else {
				t.SendMsgToTgbot(chatId, msg)
			}
			return
		} else {
			for _, traffic := range traffics {
				output += t.clientInfoMsg(traffic, true, true, true, true, true, false)
				output += "\r\n"
			}
		}
	}

	output += t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))
	if hasMessageID {
		err := t.editMessageTgBot(chatId, messageID, output, nil)
		if err != nil {
			t.SendMsgToTgbot(chatId, output)
		}
	} else {
		t.SendMsgToTgbot(chatId, output)
	}
	output = t.I18nBot("tgbot.commands.pleaseChoose")
	t.SendAnswer(chatId, output, false)
}

// getClientByEmailWithUserId retrieves a client by email and determines userId through ClientInboundMapping.
// Returns client entity, userId, and error.
func (t *Tgbot) getClientByEmailWithUserId(email string) (*model.ClientEntity, int, error) {
	clientService := ClientService{}
	db := database.GetDB()
	
	// First, try to find client by email directly (without userId)
	// We need to find userId through ClientInboundMapping
	var clientEntity model.ClientEntity
	err := db.Where("LOWER(email) = ?", strings.ToLower(email)).First(&clientEntity).Error
	if err != nil {
		return nil, 0, err
	}
	
	// Get userId from inbound mapping
	var mapping model.ClientInboundMapping
	err = db.Where("client_id = ?", clientEntity.Id).First(&mapping).Error
	if err != nil {
		// If no mapping found, try to get userId from client entity directly
		if clientEntity.UserId > 0 {
			// Reload client with full data using ClientService
			client, err := clientService.GetClientByEmail(clientEntity.UserId, email)
			if err != nil {
				return nil, 0, err
			}
			return client, clientEntity.UserId, nil
		}
		return nil, 0, gorm.ErrRecordNotFound
	}
	
	// Get inbound to get userId
	inbound, err := t.inboundService.GetInbound(mapping.InboundId)
	if err != nil {
		return nil, 0, err
	}
	
	// Reload client with full data using ClientService
	client, err := clientService.GetClientByEmail(inbound.UserId, email)
	if err != nil {
		return nil, 0, err
	}
	
	return client, inbound.UserId, nil
}

// searchClientDevices searches and sends client devices (HWID) for the given email.
func (t *Tgbot) searchClientDevices(chatId int64, email string, messageID ...int) {
	// Use ClientService to find client by email (new architecture)
	clientEntity, _, err := t.getClientByEmailWithUserId(email)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			msg := t.I18nBot("tgbot.noResult")
			if len(messageID) > 0 {
				t.editMessageTgBot(chatId, messageID[0], msg)
			} else {
				t.SendMsgToTgbot(chatId, msg)
			}
			return
		}
		logger.Warning(err)
		msg := t.I18nBot("tgbot.wentWrong")
		if len(messageID) > 0 {
			t.editMessageTgBot(chatId, messageID[0], msg)
		} else {
			t.SendMsgToTgbot(chatId, msg)
		}
		return
	}

	// Get HWIDs for this client
	hwidService := ClientHWIDService{}
	hwids, err := hwidService.GetHWIDsForClient(clientEntity.Id)
	if err != nil {
		logger.Warningf("Failed to get HWIDs for client %d: %v", clientEntity.Id, err)
	}

	output := ""
	output += t.I18nBot("tgbot.messages.email", "Email=="+email)
	
	// Show HWID status
	if clientEntity.HWIDEnabled {
		maxHwidText := "∞"
		if clientEntity.MaxHWID > 0 {
			maxHwidText = fmt.Sprintf("%d", clientEntity.MaxHWID)
		}
		hwidCount := 0
		if hwids != nil {
			hwidCount = len(hwids)
		}
		output += t.I18nBot("tgbot.messages.hwidEnabled", "Count=="+fmt.Sprintf("%d", hwidCount), "Max=="+maxHwidText)
	} else {
		output += t.I18nBot("tgbot.messages.hwidDisabled")
	}
	
	output += "\r\n\r\n"
	
	if len(hwids) == 0 {
		output += t.I18nBot("tgbot.messages.noDevices")
	} else {
		output += t.I18nBot("tgbot.messages.devicesList") + ":\r\n\r\n"
		for i, hwid := range hwids {
			status := "❌"
			if hwid.IsActive {
				status = "✅"
			}
			
			deviceInfo := fmt.Sprintf("%d. %s ", i+1, status)
			if hwid.DeviceModel != "" {
				deviceInfo += hwid.DeviceModel
			} else if hwid.DeviceOS != "" {
				deviceInfo += hwid.DeviceOS
			} else {
				deviceInfo += fmt.Sprintf("Device %d", i+1)
			}
			
			if hwid.OSVersion != "" {
				deviceInfo += " (" + hwid.OSVersion + ")"
			}
			
			deviceInfo += "\r\n"
			
			// Show HWID (shortened)
			if len(hwid.HWID) > 16 {
				deviceInfo += "   HWID: " + hwid.HWID[:16] + "...\r\n"
			} else {
				deviceInfo += "   HWID: " + hwid.HWID + "\r\n"
			}
			
			// Show first seen IP if available
			if hwid.FirstSeenIP != "" {
				deviceInfo += "   IP: " + hwid.FirstSeenIP + "\r\n"
			}
			
			// Show last seen time
			if hwid.LastSeenAt > 0 {
				lastSeen := time.Unix(hwid.LastSeenAt, 0).Format("2006-01-02 15:04:05")
				deviceInfo += "   " + t.I18nBot("tgbot.messages.lastSeen") + ": " + lastSeen + "\r\n"
			}
			
			deviceInfo += "\r\n"
			output += deviceInfo
		}
	}
	
	output += t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))

	// Get inbound ID for back button
	var mapping model.ClientInboundMapping
	db := database.GetDB()
	db.Where("client_id = ?", clientEntity.Id).First(&mapping)
	
	// Create keyboard with device buttons
	var keyboardRows [][]telego.InlineKeyboardButton
	if len(hwids) > 0 {
		// Add buttons for each device (max 5 to avoid too many buttons)
		maxDevices := 5
		if len(hwids) < maxDevices {
			maxDevices = len(hwids)
		}
		for i := 0; i < maxDevices; i++ {
			deviceLabel := fmt.Sprintf("%d. ", i+1)
			if hwids[i].DeviceModel != "" {
				deviceLabel += hwids[i].DeviceModel
			} else if hwids[i].DeviceOS != "" {
				deviceLabel += hwids[i].DeviceOS
			} else {
				deviceLabel += fmt.Sprintf("Device %d", i+1)
			}
			// Truncate label if too long
			if len(deviceLabel) > 30 {
				deviceLabel = deviceLabel[:27] + "..."
			}
			keyboardRows = append(keyboardRows, tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(deviceLabel).WithCallbackData(t.encodeQuery(fmt.Sprintf("remove_device %s %d", email, hwids[i].Id))),
			))
		}
	}
	
	// Add refresh button
	keyboardRows = append(keyboardRows, tu.InlineKeyboardRow(
		tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("devices_refresh "+email)),
	))
	
	// Add back button
	if mapping.InboundId > 0 {
		backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("get_client_info "+email))
		keyboardRows = append(keyboardRows, tu.InlineKeyboardRow(backButton))
	}
	
	inlineKeyboard := tu.InlineKeyboard(keyboardRows...)

	if len(messageID) > 0 {
		t.editMessageTgBot(chatId, messageID[0], output, inlineKeyboard)
	} else {
		t.SendMsgToTgbot(chatId, output, inlineKeyboard)
	}
}

// clientTelegramUserInfo retrieves and sends Telegram user info for the client.
func (t *Tgbot) clientTelegramUserInfo(chatId int64, email string, messageID ...int) {
	// Use ClientService to find client by email (new architecture)
	clientEntity, _, err := t.getClientByEmailWithUserId(email)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			msg := t.I18nBot("tgbot.noResult")
			if len(messageID) > 0 {
				t.editMessageTgBot(chatId, messageID[0], msg)
			} else {
				t.SendMsgToTgbot(chatId, msg)
			}
			return
		}
		logger.Warning(err)
		msg := t.I18nBot("tgbot.wentWrong")
		if len(messageID) > 0 {
			t.editMessageTgBot(chatId, messageID[0], msg)
		} else {
			t.SendMsgToTgbot(chatId, msg)
		}
		return
	}
	
	tgId := "None"
	if clientEntity.TgID != 0 {
		tgId = strconv.FormatInt(clientEntity.TgID, 10)
	}

	output := ""
	output += t.I18nBot("tgbot.messages.email", "Email=="+email)
	output += t.I18nBot("tgbot.messages.TGUser", "TelegramID=="+tgId)
	output += t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))

	// Get inbound ID for back button - find which inbound this client belongs to
	var mapping model.ClientInboundMapping
	db := database.GetDB()
	db.Where("client_id = ?", clientEntity.Id).First(&mapping)
	
	inlineKeyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("tgid_refresh "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.removeTGUser")).WithCallbackData(t.encodeQuery("tgid_remove "+email)),
		),
	)
	
	// Add back button to client info if we know the inbound ID
	if mapping.InboundId > 0 {
		backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("get_client_info "+email))
		inlineKeyboard = tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("tgid_refresh "+email)),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.removeTGUser")).WithCallbackData(t.encodeQuery("tgid_remove "+email)),
			),
			tu.InlineKeyboardRow(
				backButton,
			),
		)
	}

	if len(messageID) > 0 {
		t.editMessageTgBot(chatId, messageID[0], output, inlineKeyboard)
	} else {
		t.SendMsgToTgbot(chatId, output, inlineKeyboard)
		// Use client ID instead of traffic ID for RequestID
		requestUser := telego.KeyboardButtonRequestUsers{
			RequestID: int32(clientEntity.Id),
			UserIsBot: new(bool),
		}
		keyboard := tu.Keyboard(
			tu.KeyboardRow(
				tu.KeyboardButton(t.I18nBot("tgbot.buttons.selectTGUser")).WithRequestUsers(&requestUser),
			),
			tu.KeyboardRow(
				tu.KeyboardButton(t.I18nBot("tgbot.buttons.closeKeyboard")),
			),
		).WithIsPersistent().WithResizeKeyboard()
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.buttons.selectOneTGUser"), keyboard)
	}
}

// searchClient searches for a client by email and sends the information.
func (t *Tgbot) searchClient(chatId int64, email string, messageID ...int) {
	// Use ClientService to find client by email (new architecture)
	clientEntity, _, err := t.getClientByEmailWithUserId(email)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			msg := t.I18nBot("tgbot.noResult")
			if len(messageID) > 0 {
				t.editMessageTgBot(chatId, messageID[0], msg)
			} else {
				t.SendMsgToTgbot(chatId, msg)
			}
			return
		}
		logger.Warning(err)
		msg := t.I18nBot("tgbot.wentWrong")
		if len(messageID) > 0 {
			t.editMessageTgBot(chatId, messageID[0], msg)
		} else {
			t.SendMsgToTgbot(chatId, msg)
		}
		return
	}

	// Create ClientTraffic from ClientEntity for compatibility with clientInfoMsg
	totalBytes := int64(clientEntity.TotalGB * 1024 * 1024 * 1024)
	traffic := &xray.ClientTraffic{
		Id:         0, // Not used in new architecture
		InboundId:  0, // Will be set if needed
		Enable:     clientEntity.Enable,
		Email:      clientEntity.Email,
		UUID:       clientEntity.UUID,
		SubId:      clientEntity.SubID,
		Up:         clientEntity.Up,
		Down:       clientEntity.Down,
		AllTime:    clientEntity.AllTime,
		ExpiryTime: clientEntity.ExpiryTime,
		Total:      totalBytes,
		Reset:      clientEntity.Reset,
		LastOnline: clientEntity.LastOnline,
	}

	output := t.clientInfoMsg(traffic, true, true, true, true, true, true)

	// Get inbound ID for back button - find which inbound this client belongs to
	var mapping model.ClientInboundMapping
	db := database.GetDB()
	db.Where("client_id = ?", clientEntity.Id).First(&mapping)
	
	inlineKeyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("client_refresh "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.resetTraffic")).WithCallbackData(t.encodeQuery("reset_traffic "+email)),
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.limitTraffic")).WithCallbackData(t.encodeQuery("limit_traffic "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.resetExpire")).WithCallbackData(t.encodeQuery("reset_exp "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.deviceList")).WithCallbackData(t.encodeQuery("device_list "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.setTGUser")).WithCallbackData(t.encodeQuery("tg_user "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.toggle")).WithCallbackData(t.encodeQuery("toggle_enable "+email)),
		),
	)
	
	// Add back button if we know the inbound ID
	if mapping.InboundId > 0 {
		backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery(fmt.Sprintf("get_clients %d", mapping.InboundId)))
		inlineKeyboard = tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("client_refresh "+email)),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.resetTraffic")).WithCallbackData(t.encodeQuery("reset_traffic "+email)),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.limitTraffic")).WithCallbackData(t.encodeQuery("limit_traffic "+email)),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.resetExpire")).WithCallbackData(t.encodeQuery("reset_exp "+email)),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.deviceList")).WithCallbackData(t.encodeQuery("device_list "+email)),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.setTGUser")).WithCallbackData(t.encodeQuery("tg_user "+email)),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.toggle")).WithCallbackData(t.encodeQuery("toggle_enable "+email)),
			),
			tu.InlineKeyboardRow(
				backButton,
			),
		)
	}
	
	if len(messageID) > 0 {
		t.editMessageTgBot(chatId, messageID[0], output, inlineKeyboard)
	} else {
		t.SendMsgToTgbot(chatId, output, inlineKeyboard)
	}
}

// addClient handles the process of adding a new client to an inbound.
func (t *Tgbot) addClient(chatId int64, msg string, messageID ...int) {
	inbound, err := t.inboundService.GetInbound(receiver_inbound_ID)
	if err != nil {
		t.SendMsgToTgbot(chatId, err.Error())
		return
	}

	protocol := inbound.Protocol

	switch protocol {
	case model.VMESS, model.VLESS:
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_email")).WithCallbackData("add_client_ch_default_email"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_id")).WithCallbackData("add_client_ch_default_id"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.limitTraffic")).WithCallbackData("add_client_ch_default_traffic"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.resetExpire")).WithCallbackData("add_client_ch_default_exp"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_comment")).WithCallbackData("add_client_ch_default_comment"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.hwidLimit")).WithCallbackData("add_client_ch_default_hwid"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.submitDisable")).WithCallbackData("add_client_submit_disable"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.submitEnable")).WithCallbackData("add_client_submit_enable"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData("add_client_cancel"),
			),
		)
		if len(messageID) > 0 {
			t.editMessageTgBot(chatId, messageID[0], msg, inlineKeyboard)
		} else {
			t.SendMsgToTgbot(chatId, msg, inlineKeyboard)
		}
	case model.Trojan:
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_email")).WithCallbackData("add_client_ch_default_email"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_password")).WithCallbackData("add_client_ch_default_pass_tr"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.limitTraffic")).WithCallbackData("add_client_ch_default_traffic"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.resetExpire")).WithCallbackData("add_client_ch_default_exp"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_comment")).WithCallbackData("add_client_ch_default_comment"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.hwidLimit")).WithCallbackData("add_client_ch_default_hwid"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.submitDisable")).WithCallbackData("add_client_submit_disable"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.submitEnable")).WithCallbackData("add_client_submit_enable"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData("add_client_cancel"),
			),
		)
		if len(messageID) > 0 {
			t.editMessageTgBot(chatId, messageID[0], msg, inlineKeyboard)
		} else {
			t.SendMsgToTgbot(chatId, msg, inlineKeyboard)
		}
	case model.Shadowsocks:
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_email")).WithCallbackData("add_client_ch_default_email"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_password")).WithCallbackData("add_client_ch_default_pass_sh"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.limitTraffic")).WithCallbackData("add_client_ch_default_traffic"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.resetExpire")).WithCallbackData("add_client_ch_default_exp"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_comment")).WithCallbackData("add_client_ch_default_comment"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.hwidLimit")).WithCallbackData("add_client_ch_default_hwid"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.submitDisable")).WithCallbackData("add_client_submit_disable"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.submitEnable")).WithCallbackData("add_client_submit_enable"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData("add_client_cancel"),
			),
		)

		if len(messageID) > 0 {
			t.editMessageTgBot(chatId, messageID[0], msg, inlineKeyboard)
		} else {
			t.SendMsgToTgbot(chatId, msg, inlineKeyboard)
		}
	}

}

// searchInbound searches for inbounds by remark and sends the results.
func (t *Tgbot) searchInbound(chatId int64, remark string) {
	inbounds, err := t.inboundService.SearchInbounds(remark)
	if err != nil {
		logger.Warning(err)
		msg := t.I18nBot("tgbot.wentWrong")
		t.SendMsgToTgbot(chatId, msg)
		return
	}
	if len(inbounds) == 0 {
		msg := t.I18nBot("tgbot.noInbounds")
		t.SendMsgToTgbot(chatId, msg)
		return
	}

	for _, inbound := range inbounds {
		info := ""
		info += t.I18nBot("tgbot.messages.inbound", "Remark=="+inbound.Remark)
		info += t.I18nBot("tgbot.messages.port", "Port=="+strconv.Itoa(inbound.Port))
		info += t.I18nBot("tgbot.messages.traffic", "Total=="+common.FormatTraffic((inbound.Up+inbound.Down)), "Upload=="+common.FormatTraffic(inbound.Up), "Download=="+common.FormatTraffic(inbound.Down))

		if inbound.ExpiryTime == 0 {
			info += t.I18nBot("tgbot.messages.expire", "Time=="+t.I18nBot("tgbot.unlimited"))
		} else {
			info += t.I18nBot("tgbot.messages.expire", "Time=="+time.Unix((inbound.ExpiryTime/1000), 0).Format("2006-01-02 15:04:05"))
		}
		t.SendMsgToTgbot(chatId, info)

		if len(inbound.ClientStats) > 0 {
			output := ""
			for _, traffic := range inbound.ClientStats {
				output += t.clientInfoMsg(&traffic, true, true, true, true, true, true)
			}
			t.SendMsgToTgbot(chatId, output)
		}
	}
}

// getExhausted retrieves and sends information about exhausted clients.
func (t *Tgbot) getExhausted(chatId int64, messageID ...int) {
	trDiff := int64(0)
	exDiff := int64(0)
	now := time.Now().Unix() * 1000
	var exhaustedInbounds []model.Inbound
	var exhaustedClients []xray.ClientTraffic
	var disabledInbounds []model.Inbound
	var disabledClients []xray.ClientTraffic

	TrafficThreshold, err := t.settingService.GetTrafficDiff()
	if err == nil && TrafficThreshold > 0 {
		trDiff = int64(TrafficThreshold) * 1073741824
	}
	ExpireThreshold, err := t.settingService.GetExpireDiff()
	if err == nil && ExpireThreshold > 0 {
		exDiff = int64(ExpireThreshold) * 86400000
	}
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("Unable to load Inbounds", err)
	}

	for _, inbound := range inbounds {
		if inbound.Enable {
			if (inbound.ExpiryTime > 0 && (inbound.ExpiryTime-now < exDiff)) ||
				(inbound.Total > 0 && (inbound.Total-(inbound.Up+inbound.Down) < trDiff)) {
				exhaustedInbounds = append(exhaustedInbounds, *inbound)
			}
			if len(inbound.ClientStats) > 0 {
				for _, client := range inbound.ClientStats {
					if client.Enable {
						if (client.ExpiryTime > 0 && (client.ExpiryTime-now < exDiff)) ||
							(client.Total > 0 && (client.Total-(client.Up+client.Down) < trDiff)) {
							exhaustedClients = append(exhaustedClients, client)
						}
					} else {
						disabledClients = append(disabledClients, client)
					}
				}
			}
		} else {
			disabledInbounds = append(disabledInbounds, *inbound)
		}
	}

	// Inbounds
	output := ""
	output += t.I18nBot("tgbot.messages.exhaustedCount", "Type=="+t.I18nBot("tgbot.inbounds"))
	output += t.I18nBot("tgbot.messages.disabled", "Disabled=="+strconv.Itoa(len(disabledInbounds)))
	output += t.I18nBot("tgbot.messages.depleteSoon", "Deplete=="+strconv.Itoa(len(exhaustedInbounds)))

	if len(exhaustedInbounds) > 0 {
		output += t.I18nBot("tgbot.messages.depleteSoon", "Deplete=="+t.I18nBot("tgbot.inbounds"))

		for _, inbound := range exhaustedInbounds {
			output += t.I18nBot("tgbot.messages.inbound", "Remark=="+inbound.Remark)
			output += t.I18nBot("tgbot.messages.port", "Port=="+strconv.Itoa(inbound.Port))
			output += t.I18nBot("tgbot.messages.traffic", "Total=="+common.FormatTraffic((inbound.Up+inbound.Down)), "Upload=="+common.FormatTraffic(inbound.Up), "Download=="+common.FormatTraffic(inbound.Down))
			if inbound.ExpiryTime == 0 {
				output += t.I18nBot("tgbot.messages.expire", "Time=="+t.I18nBot("tgbot.unlimited"))
			} else {
				output += t.I18nBot("tgbot.messages.expire", "Time=="+time.Unix((inbound.ExpiryTime/1000), 0).Format("2006-01-02 15:04:05"))
			}
			output += "\r\n"
		}
	}

	// Clients
	exhaustedCC := len(exhaustedClients)
	output += t.I18nBot("tgbot.messages.exhaustedCount", "Type=="+t.I18nBot("tgbot.clients"))
	output += t.I18nBot("tgbot.messages.disabled", "Disabled=="+strconv.Itoa(len(disabledClients)))
	output += t.I18nBot("tgbot.messages.depleteSoon", "Deplete=="+strconv.Itoa(exhaustedCC))

	if exhaustedCC > 0 {
		output += t.I18nBot("tgbot.messages.depleteSoon", "Deplete=="+t.I18nBot("tgbot.clients"))
		var buttons []telego.InlineKeyboardButton
		for _, traffic := range exhaustedClients {
			output += t.clientInfoMsg(&traffic, true, false, false, true, true, false)
			output += "\r\n"
			buttons = append(buttons, tu.InlineKeyboardButton(traffic.Email).WithCallbackData(t.encodeQuery("client_get_usage "+traffic.Email)))
		}
		cols := 0
		if exhaustedCC < 11 {
			cols = 1
		} else {
			cols = 2
		}
		output += t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))
		// Add back button to main menu
		backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("back_to_main"))
		buttons = append(buttons, backButton)
		keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols, buttons...))
		if len(messageID) > 0 {
			err := t.editMessageTgBot(chatId, messageID[0], output, keyboard)
			if err != nil {
				t.SendMsgToTgbot(chatId, output, keyboard)
			}
		} else {
			t.SendMsgToTgbot(chatId, output, keyboard)
		}
	} else {
		output += t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))
		// Add back button to main menu
		backKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("back_to_main")),
			),
		)
		if len(messageID) > 0 {
			err := t.editMessageTgBot(chatId, messageID[0], output, backKeyboard)
			if err != nil {
				t.SendMsgToTgbot(chatId, output, backKeyboard)
			}
		} else {
			t.SendMsgToTgbot(chatId, output, backKeyboard)
		}
	}
}

// notifyExhausted sends notifications for exhausted clients.
func (t *Tgbot) notifyExhausted() {
	trDiff := int64(0)
	exDiff := int64(0)
	now := time.Now().Unix() * 1000

	TrafficThreshold, err := t.settingService.GetTrafficDiff()
	if err == nil && TrafficThreshold > 0 {
		trDiff = int64(TrafficThreshold) * 1073741824
	}
	ExpireThreshold, err := t.settingService.GetExpireDiff()
	if err == nil && ExpireThreshold > 0 {
		exDiff = int64(ExpireThreshold) * 86400000
	}
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("Unable to load Inbounds", err)
	}

	var chatIDsDone []int64
	for _, inbound := range inbounds {
		if inbound.Enable {
			if len(inbound.ClientStats) > 0 {
				clients, err := t.inboundService.GetClients(inbound)
				if err == nil {
					for _, client := range clients {
						if client.TgID != 0 {
							chatID := client.TgID
							if !int64Contains(chatIDsDone, chatID) && !checkAdmin(chatID) {
								var disabledClients []xray.ClientTraffic
								var exhaustedClients []xray.ClientTraffic
								// Try new architecture first
								traffics, err := t.getClientTrafficTgBotNew(client.TgID)
								if err != nil || len(traffics) == 0 {
									// Fallback to old method
									traffics, err = t.inboundService.GetClientTrafficTgBot(client.TgID)
								}
								if err == nil && len(traffics) > 0 {
									output := t.I18nBot("tgbot.messages.exhaustedCount", "Type=="+t.I18nBot("tgbot.clients"))
									for _, traffic := range traffics {
										if traffic.Enable {
											if (traffic.ExpiryTime > 0 && (traffic.ExpiryTime-now < exDiff)) ||
												(traffic.Total > 0 && (traffic.Total-(traffic.Up+traffic.Down) < trDiff)) {
												exhaustedClients = append(exhaustedClients, *traffic)
											}
										} else {
											disabledClients = append(disabledClients, *traffic)
										}
									}
									if len(exhaustedClients) > 0 {
										output += t.I18nBot("tgbot.messages.disabled", "Disabled=="+strconv.Itoa(len(disabledClients)))
										if len(disabledClients) > 0 {
											output += t.I18nBot("tgbot.clients") + ":\r\n"
											for _, traffic := range disabledClients {
												output += " " + traffic.Email
											}
											output += "\r\n"
										}
										output += "\r\n"
										output += t.I18nBot("tgbot.messages.depleteSoon", "Deplete=="+strconv.Itoa(len(exhaustedClients)))
										for _, traffic := range exhaustedClients {
											output += t.clientInfoMsg(&traffic, true, false, false, true, true, false)
											output += "\r\n"
										}
										t.SendMsgToTgbot(chatID, output)
									}
									chatIDsDone = append(chatIDsDone, chatID)
								}
							}
						}
					}
				}
			}
		}
	}
}

// int64Contains checks if an int64 slice contains a specific item.
func int64Contains(slice []int64, item int64) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// onlineClients retrieves and sends information about online clients.
func (t *Tgbot) onlineClients(chatId int64, messageID ...int) {
	if !p.IsRunning() {
		return
	}

	onlines := p.GetOnlineClients()
	onlinesCount := len(onlines)
	output := t.I18nBot("tgbot.messages.onlinesCount", "Count=="+fmt.Sprint(onlinesCount))
	keyboard := tu.InlineKeyboard(tu.InlineKeyboardRow(
		tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("onlines_refresh"))))

	if onlinesCount > 0 {
		var buttons []telego.InlineKeyboardButton
		for _, online := range onlines {
			buttons = append(buttons, tu.InlineKeyboardButton(online).WithCallbackData(t.encodeQuery("client_get_usage "+online)))
		}
		cols := 0
		if onlinesCount < 21 {
			cols = 2
		} else if onlinesCount < 61 {
			cols = 3
		} else {
			cols = 4
		}
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, tu.InlineKeyboardCols(cols, buttons...)...)
	}
	
	// Add back button to main menu
	backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("back_to_main"))
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, tu.InlineKeyboardRow(backButton))

	if len(messageID) > 0 {
		t.editMessageTgBot(chatId, messageID[0], output, keyboard)
	} else {
		t.SendMsgToTgbot(chatId, output, keyboard)
	}
}

// sendBackup sends a backup of the database and configuration files.
func (t *Tgbot) sendBackup(chatId int64) {
	output := t.I18nBot("tgbot.messages.backupTime", "Time=="+time.Now().Format("2006-01-02 15:04:05"))
	t.SendMsgToTgbot(chatId, output)

	// Create a temporary file for the database backup
	tempFile, err := os.CreateTemp("", "x-ui-db-backup-*.sql")
	if err != nil {
		logger.Error("Error creating temporary backup file: ", err)
		return
	}
	tempPath := tempFile.Name()
	defer func() {
		tempFile.Close()
		os.Remove(tempPath)
	}()

	// Get database connection parameters
	host := config.GetDBHost()
	port := config.GetDBPort()
	user := config.GetDBUser()
	password := config.GetDBPassword()
	dbname := config.GetDBName()

	// Set PGPASSWORD environment variable for pg_dump
	env := os.Environ()
	env = append(env, fmt.Sprintf("PGPASSWORD=%s", password))

	// Use pg_dump to create a backup with --clean and --if-exists for proper restore
	cmd := exec.Command("pg_dump",
		"-h", host,
		"-p", strconv.Itoa(port),
		"-U", user,
		"-d", dbname,
		"--format=plain",
		"--no-owner",
		"--no-privileges",
		"--clean",
		"--if-exists",
	)
	cmd.Env = env
	cmd.Stdout = tempFile

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		// pg_dump failed, try fallback method via ServerService
		logger.Warningf("pg_dump not available for Telegram backup, falling back to GORM-based export: %v", err)
		dbData, fallbackErr := t.serverService.GetDb()
		if fallbackErr != nil {
			logger.Errorf("Error in database backup (both pg_dump and fallback failed): %v", fallbackErr)
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.messages.backupError"))
			return
		}
		// Write fallback data to temp file
		if _, err = tempFile.Write(dbData); err != nil {
			logger.Error("Error writing fallback backup data to file: ", err)
			return
		}
	} else {
		// pg_dump succeeded, data already in tempFile
	}

	// Close the file before sending
	if err = tempFile.Close(); err != nil {
		logger.Error("Error closing temporary backup file: ", err)
		return
	}

	// Read the file content and send it
	fileBytes, err := os.ReadFile(tempPath)
	if err != nil {
		logger.Error("Error reading backup file: ", err)
		return
	}

	document := tu.Document(
		tu.ID(chatId),
		tu.FileFromBytes(fileBytes, "x-ui-db-backup.sql"),
	)
	_, err = bot.SendDocument(context.Background(), document)
	if err != nil {
		logger.Error("Error in uploading backup: ", err)
	}

	// Check if multi-node mode is enabled before trying to open config.json
	multiMode, err := t.settingService.GetMultiNodeMode()
	if err == nil && !multiMode {
		file, err := os.Open(xray.GetConfigPath())
		if err == nil {
			document := tu.Document(
				tu.ID(chatId),
				tu.File(file),
			)
			_, err = bot.SendDocument(context.Background(), document)
			if err != nil {
				logger.Error("Error in uploading config.json: ", err)
			}
		} else {
			logger.Error("Error in opening config.json file for backup: ", err)
		}
	} else if multiMode {
		logger.Debug("Skipping config.json backup in multi-node mode")
	}
}


// sendCallbackAnswerTgBot answers a callback query with a message.
func (t *Tgbot) sendCallbackAnswerTgBot(id string, message string) {
	params := telego.AnswerCallbackQueryParams{
		CallbackQueryID: id,
		Text:            message,
	}
	if err := bot.AnswerCallbackQuery(context.Background(), &params); err != nil {
		logger.Warning(err)
	}
}

// editMessageCallbackTgBot edits the reply markup of a message.
func (t *Tgbot) editMessageCallbackTgBot(chatId int64, messageID int, inlineKeyboard *telego.InlineKeyboardMarkup) {
	params := telego.EditMessageReplyMarkupParams{
		ChatID:      tu.ID(chatId),
		MessageID:   messageID,
		ReplyMarkup: inlineKeyboard,
	}
	if _, err := bot.EditMessageReplyMarkup(context.Background(), &params); err != nil {
		logger.Warning(err)
	}
}

// editMessageTgBot edits the text and reply markup of a message.
// Returns error if edit fails (e.g., message doesn't exist).
func (t *Tgbot) editMessageTgBot(chatId int64, messageID int, text string, inlineKeyboard ...*telego.InlineKeyboardMarkup) error {
	params := telego.EditMessageTextParams{
		ChatID:    tu.ID(chatId),
		MessageID: messageID,
		Text:      text,
		ParseMode: "HTML",
	}
	if len(inlineKeyboard) > 0 {
		params.ReplyMarkup = inlineKeyboard[0]
	}
	_, err := bot.EditMessageText(context.Background(), &params)
	if err != nil {
		// Check if error is "message is not modified" - this is OK, means content is the same
		if strings.Contains(err.Error(), "message is not modified") || strings.Contains(err.Error(), "message not modified") {
			logger.Debugf("Message %d not modified (content unchanged), this is OK", messageID)
			return nil
		}
		logger.Debugf("Failed to edit message %d: %v", messageID, err)
		return err
	}
	return nil
}

// SendMsgToTgbotDeleteAfter sends a message and deletes it after a specified delay.
func (t *Tgbot) SendMsgToTgbotDeleteAfter(chatId int64, msg string, delayInSeconds int, replyMarkup ...telego.ReplyMarkup) {
	// Determine if replyMarkup was passed; otherwise, set it to nil
	var replyMarkupParam telego.ReplyMarkup
	if len(replyMarkup) > 0 {
		replyMarkupParam = replyMarkup[0] // Use the first element
	}

	// Send the message
	sentMsg, err := bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:      tu.ID(chatId),
		Text:        msg,
		ReplyMarkup: replyMarkupParam, // Use the correct replyMarkup value
	})
	if err != nil {
		logger.Warning("Failed to send message:", err)
		return
	}

	// Delete the sent message after the specified number of seconds
	go func() {
		time.Sleep(time.Duration(delayInSeconds) * time.Second) // Wait for the specified delay
		t.deleteMessageTgBot(chatId, sentMsg.MessageID)         // Delete the message
		delete(userStates, chatId)
	}()
}

// deleteMessageTgBot deletes a message from the chat.
func (t *Tgbot) deleteMessageTgBot(chatId int64, messageID int) {
	params := telego.DeleteMessageParams{
		ChatID:    tu.ID(chatId),
		MessageID: messageID,
	}
	if err := bot.DeleteMessage(context.Background(), &params); err != nil {
		logger.Warning("Failed to delete message:", err)
	} else {
		logger.Info("Message deleted successfully")
	}
}

// createBackAndDeleteKeyboard creates an inline keyboard with "Back" and "Delete" buttons.
// If messageID is 0, the delete button will be disabled (for messages not yet sent).
func (t *Tgbot) createBackAndDeleteKeyboard(chatId int64, messageID int) *telego.InlineKeyboardMarkup {
	var buttons []telego.InlineKeyboardButton
	
	// Add back button
	backButton := tu.InlineKeyboardButton("◀️ " + t.I18nBot("tgbot.buttons.back")).WithCallbackData(t.encodeQuery("back_to_main"))
	buttons = append(buttons, backButton)
	
	// Add delete button if messageID is provided
	if messageID > 0 {
		deleteCallbackData := t.encodeQuery(fmt.Sprintf("delete_message %d", messageID))
		deleteButton := tu.InlineKeyboardButton("🗑️ " + t.I18nBot("tgbot.buttons.delete")).WithCallbackData(deleteCallbackData)
		buttons = append(buttons, deleteButton)
	}
	
	return tu.InlineKeyboard(tu.InlineKeyboardRow(buttons...))
}

// sendMessageWithKeyboard sends a message with keyboard and returns the sent message.
func (t *Tgbot) sendMessageWithKeyboard(chatId int64, msg string, replyMarkup telego.ReplyMarkup) (*telego.Message, error) {
	if !isRunning {
		return nil, errors.New("bot is not running")
	}

	if msg == "" {
		logger.Info("[tgbot] message is empty!")
		return nil, errors.New("message is empty")
	}

	params := telego.SendMessageParams{
		ChatID:    tu.ID(chatId),
		Text:      msg,
		ParseMode: "HTML",
	}
	if replyMarkup != nil {
		params.ReplyMarkup = replyMarkup
	}
	
	sentMsg, err := bot.SendMessage(context.Background(), &params)
	if err != nil {
		logger.Warning("Error sending telegram message:", err)
		return nil, err
	}
	
	return sentMsg, nil
}

// isSingleWord checks if the text contains only a single word.
func (t *Tgbot) isSingleWord(text string) bool {
	text = strings.TrimSpace(text)
	re := regexp.MustCompile(`\s+`)
	return re.MatchString(text)
}
