// Package karakeepbot implements a Telegram bot that allows users to create
// bookmarks through messages. The bot interacts with the Karakeep API to manage
// bookmarks and handles incoming messages by checking if the chat ID is
// allowed, creating bookmarks, and sending back updated messages with tags.
package karakeepbot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/Madh93/karakeepbot/internal/config"
	"github.com/Madh93/karakeepbot/internal/fileprocessor"
	"github.com/Madh93/karakeepbot/internal/filevalidator"
	"github.com/Madh93/karakeepbot/internal/logging"
	"github.com/Madh93/karakeepbot/internal/validation"
	"github.com/go-telegram/bot/models"
)

const (
	successReaction = "👍"
	failureReaction = "👎"
)

// KarakeepBot represents the bot with its dependencies, including the Karakeep
// client, Telegram bot, logger and other options.
type KarakeepBot struct {
	karakeep       *Karakeep
	telegram       *Telegram
	logger         *logging.Logger
	fileProcessor  *fileprocessor.Processor
	fileValidators map[string]fileprocessor.Validator
	allowlist      []int64
	threads        []int
}

// New creates a new KarakeepBot instance, initializing the Karakeep and Telegram
// clients.
func New(logger *logging.Logger, config *config.Config) *KarakeepBot {
	// Initialize FileProcessor
	fileProcessor, err := fileprocessor.New(&config.FileProcessor)
	if err != nil {
		logger.Fatal("Failed to create file processor", "error", err)
	}

	// Setup Supported File Validators
	fileValidators := make(map[string]fileprocessor.Validator)
	fileValidators["image/jpeg"] = filevalidator.ImageValidator
	fileValidators["image/png"] = filevalidator.ImageValidator
	fileValidators["image/webp"] = filevalidator.ImageValidator

	// Check if the validators passed in the configuration are supported
	if len(config.FileProcessor.Mimetypes) > 0 {
		for _, mimetype := range config.FileProcessor.Mimetypes {
			if _, supported := fileValidators[mimetype]; !supported {
				logger.Fatal("Configuration error: unsupported MIME type configured", "mime_type", mimetype)
			}
		}
	}

	return &KarakeepBot{
		karakeep:       createKarakeep(logger, &config.Karakeep),
		telegram:       createTelegram(logger, &config.Telegram),
		allowlist:      config.Telegram.Allowlist,
		threads:        config.Telegram.Threads,
		fileProcessor:  fileProcessor,
		fileValidators: fileValidators,
		logger:         logger,
	}
}

// Run starts the bot and handles incoming messages.
func (kb *KarakeepBot) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Set default handler
	kb.telegram.RegisterHandlerMatchFunc(func(*TelegramUpdate) bool { return true }, kb.handler)

	// Start the bot
	kb.telegram.Start(ctx)

	return nil
}

// handler is the main handler for incoming messages. It processes the message
// and sends a response back to the user.
func (kb KarakeepBot) handler(ctx context.Context, _ *Bot, update *TelegramUpdate) {
	if update.Message == nil {
		return
	}

	msg := TelegramMessage(*update.Message)

	// Check if the chat ID is allowed
	if !kb.isChatIdAllowed(msg.Chat.ID) {
		kb.logger.Warn(fmt.Sprintf("Received message from not allowed chat ID. Allowed chats IDs: %v", kb.allowlist), msg.Attrs()...)
		return
	}

	// Check if the thread ID is allowed
	if !kb.isThreadIdAllowed(msg.MessageThreadID) {
		kb.logger.Warn(fmt.Sprintf("Received message from not allowed thread ID. Allowed thread IDs: %v", kb.threads), msg.Attrs()...)
		return
	}

	kb.logger.Debug("Received message from allowed chat ID and allowed thread ID", msg.Attrs()...)

	// Parse the message to get corresponding bookmark types
	kb.logger.Debug("Parsing message to get corresponding bookmark types", msg.Attrs()...)
	bookmarks, err := kb.parseMessage(ctx, msg)
	if err != nil {
		kb.logger.Error("Failed to parse message", msg.AttrsWithError(err)...)
		kb.react(ctx, &msg, failureReaction)
		return
	}

	for _, b := range bookmarks {
		kb.logger.Debug(fmt.Sprintf("Creating bookmark of type %s", b))
		bookmark, err := kb.karakeep.CreateBookmark(ctx, b)
		if err != nil {
			kb.logger.Error("Failed to create bookmark", "error", err)
			kb.react(ctx, &msg, failureReaction)
			return
		}
		kb.logger.Info("Created bookmark", bookmark.Attrs()...)
	}

	kb.react(ctx, &msg, successReaction)
	kb.logger.Info("Processed message", msg.Attrs()...)
}

func (kb KarakeepBot) react(ctx context.Context, msg *TelegramMessage, emoji string) {
	if err := kb.telegram.SetReaction(ctx, msg, emoji); err != nil {
		kb.logger.Error("Failed to set reaction", msg.AttrsWithError(err)...)
	}
}

// isChatIdAllowed checks if the chat ID is allowed to receive messages.
func (kb KarakeepBot) isChatIdAllowed(chatId int64) bool {
	// When no allowlist is provided, all chat IDs are allowed
	if len(kb.allowlist) == 0 {
		return true
	}

	// When the allowlist provided by environment variable is empty, it contains
	// a single element with value 0.
	if len(kb.allowlist) == 1 && kb.allowlist[0] == 0 {
		return true
	}

	return slices.Contains(kb.allowlist, chatId)
}

// isThreadIdAllowed checks if the thread ID is allowed to receive messages.
func (kb KarakeepBot) isThreadIdAllowed(threadId int) bool {
	return len(kb.threads) == 0 || slices.Contains(kb.threads, threadId)
}

// parseMessage parses the incoming Telegram message and returns corresponding Bookmark types.
func (kb KarakeepBot) parseMessage(ctx context.Context, msg TelegramMessage) ([]BookmarkType, error) {
	text := msg.Text
	entities := msg.Entities
	if strings.TrimSpace(text) == "" {
		text = msg.Caption
		entities = msg.CaptionEntities
	}

	if strings.TrimSpace(text) != "" {
		return parseTextBookmarks(text, entities), nil
	}

	if msg.Photo != nil {
		bookmark, err := kb.handlePhotoMessage(ctx, msg)
		if err != nil {
			return nil, err
		}
		return []BookmarkType{bookmark}, nil
	}

	return nil, errors.New("unsupported bookmark type")
}

func parseTextBookmarks(text string, entities []models.MessageEntity) []BookmarkType {
	links := extractLinks(text, entities)
	trimmedText := strings.TrimSpace(text)
	if validation.ValidateURL(trimmedText) == nil {
		return []BookmarkType{NewLinkBookmark(trimmedText)}
	}

	bookmarks := []BookmarkType{NewTextBookmark(text)}
	for _, link := range links {
		bookmarks = append(bookmarks, NewLinkBookmark(link))
	}
	return bookmarks
}

func extractLinks(text string, entities []models.MessageEntity) []string {
	seen := map[string]struct{}{}
	links := make([]string, 0, len(entities))

	for _, entity := range entities {
		var link string
		switch entity.Type {
		case models.MessageEntityTypeURL:
			link = substringByUTF16Offset(text, entity.Offset, entity.Length)
		case models.MessageEntityTypeTextLink:
			link = entity.URL
		default:
			continue
		}

		link = strings.TrimSpace(link)
		if validation.ValidateURL(link) != nil {
			continue
		}
		if _, ok := seen[link]; ok {
			continue
		}
		seen[link] = struct{}{}
		links = append(links, link)
	}

	return links
}

func substringByUTF16Offset(text string, offset int, length int) string {
	if offset < 0 || length < 0 {
		return ""
	}

	start := -1
	end := -1
	pos := 0
	runes := []rune(text)
	for i, r := range runes {
		if pos == offset {
			start = i
		}
		pos += utf16.RuneLen(r)
		if pos == offset+length {
			end = i + 1
			break
		}
	}
	if pos == offset && start == -1 {
		start = len(runes)
	}
	if pos == offset+length && end == -1 {
		end = len(runes)
	}
	if start == -1 || end == -1 || start > end {
		return ""
	}

	return string(runes[start:end])
}

// handlePhotoMessage processes a message containing a photo.
func (kb *KarakeepBot) handlePhotoMessage(ctx context.Context, msg TelegramMessage) (bookmark BookmarkType, err error) {
	// Select the largest photo
	photo := msg.Photo[len(msg.Photo)-1]
	kb.logger.Debug("Handling Telegram image", "file_id", photo.FileID, "file_size", photo.FileSize)

	// Get file URL
	fileURL, err := kb.telegram.GetFileURL(ctx, photo.FileID)
	if err != nil {
		kb.logger.Error("Failed to get file URL", msg.AttrsWithError(err)...)
		return nil, errors.New("couldn't get file URL")
	}

	// Download file. NOTE: Telegram Photo does not have mime type info. We can't use any validator.
	filePath, mimeType, err := kb.fileProcessor.Process(fileURL, nil)
	if err != nil {
		kb.logger.Error("Failed to process image", msg.AttrsWithError(err)...)
		return nil, errors.New("couldn't process image")
	}
	defer func() {
		if cleanupErr := kb.fileProcessor.Cleanup(filePath); cleanupErr != nil {
			kb.logger.Error("Failed to cleanup temporary file", "path", filePath, "error", cleanupErr)
			if err == nil {
				err = cleanupErr
			}
		}
	}()

	kb.logger.Debug("Detected MIME type", "mime_type", mimeType)

	// Upload asset to Karakeep
	asset, err := kb.karakeep.CreateAsset(ctx, filePath, mimeType)
	if err != nil {
		kb.logger.Error("Failed to upload asset", msg.AttrsWithError(err)...)
		return nil, errors.New("couldn't upload asset")
	}

	kb.logger.Debug("Asset uploaded successfully", "asset_id", asset.AssetId)

	// Get note from caption
	note := strings.TrimSpace(msg.Caption)

	return NewAssetBookmark(asset.AssetId, ImageAssetType, note), nil
}
