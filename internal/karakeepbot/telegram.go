package karakeepbot

import (
	"context"
	"fmt"

	"github.com/Madh93/karakeepbot/internal/config"
	"github.com/Madh93/karakeepbot/internal/logging"
	"github.com/Madh93/karakeepbot/internal/secret"
	tgbotapi "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Bot is an alias for tgbotapi.Bot.
type Bot = tgbotapi.Bot

// Telegram embeds the Telegram bot API client to add high level functionality.
type Telegram struct {
	*Bot
	token secret.String
}

// createTelegram initializes the Telegram Bot API client.
func createTelegram(logger *logging.Logger, config *config.TelegramConfig) *Telegram {
	logger.Debug(fmt.Sprintf("Initializing Telegram Bot API using %s token", config.Token))

	telegramBot, err := tgbotapi.New(config.Token.Value())
	if err != nil {
		logger.Fatal("Error creating Telegram Bot API.", "error", err)
	}

	return &Telegram{Bot: telegramBot, token: config.Token}
}

// SetReaction sets an emoji reaction on a message.
func (t Telegram) SetReaction(ctx context.Context, msg *TelegramMessage, emoji string) error {
	params := &tgbotapi.SetMessageReactionParams{
		ChatID:    msg.Chat.ID,
		MessageID: msg.ID,
		Reaction: []models.ReactionType{
			{
				Type: models.ReactionTypeTypeEmoji,
				ReactionTypeEmoji: &models.ReactionTypeEmoji{
					Emoji: emoji,
				},
			},
		},
	}

	if _, err := t.Bot.SetMessageReaction(ctx, params); err != nil {
		return err
	}

	return nil
}

// GetFileURL returns the download URL for a given file ID.
func (t Telegram) GetFileURL(ctx context.Context, fileID string) (string, error) {
	file, err := t.GetFile(ctx, &tgbotapi.GetFileParams{FileID: fileID})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", t.token.Value(), file.FilePath), nil
}
