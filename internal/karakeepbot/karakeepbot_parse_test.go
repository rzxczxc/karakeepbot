package karakeepbot

import (
	"context"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestExtractLinks(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		entities []models.MessageEntity
		expected []string
	}{
		{
			name: "plain URL entity",
			text: "read https://example.com/a",
			entities: []models.MessageEntity{
				{Type: models.MessageEntityTypeURL, Offset: 5, Length: 21},
			},
			expected: []string{"https://example.com/a"},
		},
		{
			name: "text link entity",
			text: "read this",
			entities: []models.MessageEntity{
				{Type: models.MessageEntityTypeTextLink, Offset: 5, Length: 4, URL: "https://example.com/hidden"},
			},
			expected: []string{"https://example.com/hidden"},
		},
		{
			name: "deduplicates and ignores non-http links",
			text: "a https://example.com/a b https://example.com/a c",
			entities: []models.MessageEntity{
				{Type: models.MessageEntityTypeURL, Offset: 2, Length: 21},
				{Type: models.MessageEntityTypeURL, Offset: 26, Length: 21},
				{Type: models.MessageEntityTypeTextLink, Offset: 0, Length: 1, URL: "ftp://example.com/file"},
			},
			expected: []string{"https://example.com/a"},
		},
		{
			name: "uses UTF-16 offsets",
			text: "go 😀 https://example.com/a",
			entities: []models.MessageEntity{
				{Type: models.MessageEntityTypeURL, Offset: 6, Length: 21},
			},
			expected: []string{"https://example.com/a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLinks(tt.text, tt.entities)
			if len(got) != len(tt.expected) {
				t.Fatalf("expected %d links, got %d: %#v", len(tt.expected), len(got), got)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Fatalf("expected link %d to be %q, got %q", i, tt.expected[i], got[i])
				}
			}
		})
	}
}

func TestParseMessageCreatesTextAndSeparateLinks(t *testing.T) {
	msg := TelegramMessage(models.Message{
		Text: "read https://example.com/a and hidden",
		Entities: []models.MessageEntity{
			{Type: models.MessageEntityTypeURL, Offset: 5, Length: 21},
			{Type: models.MessageEntityTypeTextLink, Offset: 31, Length: 6, URL: "https://example.com/b"},
		},
	})

	bookmarks, err := (KarakeepBot{}).parseMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("parseMessage returned error: %v", err)
	}

	assertBookmarks(t, bookmarks, []BookmarkType{
		NewTextBookmark("read https://example.com/a and hidden"),
		NewLinkBookmark("https://example.com/a"),
		NewLinkBookmark("https://example.com/b"),
	})
}

func TestParseMessageKeepsURLOnlyAsLink(t *testing.T) {
	msg := TelegramMessage(models.Message{Text: " https://example.com/a "})

	bookmarks, err := (KarakeepBot{}).parseMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("parseMessage returned error: %v", err)
	}

	assertBookmarks(t, bookmarks, []BookmarkType{
		NewLinkBookmark("https://example.com/a"),
	})
}

func TestParseMessagePreservesTextWhitespace(t *testing.T) {
	msg := TelegramMessage(models.Message{Text: "\nforwarded text\n"})

	bookmarks, err := (KarakeepBot{}).parseMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("parseMessage returned error: %v", err)
	}

	assertBookmarks(t, bookmarks, []BookmarkType{
		NewTextBookmark("\nforwarded text\n"),
	})
}

func TestParseMessagePhotoWithCaptionSkipsImageAsset(t *testing.T) {
	msg := TelegramMessage(models.Message{
		Caption: "caption https://example.com/a",
		CaptionEntities: []models.MessageEntity{
			{Type: models.MessageEntityTypeURL, Offset: 8, Length: 21},
		},
		Photo: []models.PhotoSize{{FileID: "photo-id"}},
	})

	bookmarks, err := (KarakeepBot{}).parseMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("parseMessage returned error: %v", err)
	}

	assertBookmarks(t, bookmarks, []BookmarkType{
		NewTextBookmark("caption https://example.com/a"),
		NewLinkBookmark("https://example.com/a"),
	})
}

func assertBookmarks(t *testing.T, got []BookmarkType, expected []BookmarkType) {
	t.Helper()

	if len(got) != len(expected) {
		t.Fatalf("expected %d bookmarks, got %d: %#v", len(expected), len(got), got)
	}
	for i := range got {
		switch want := expected[i].(type) {
		case *TextBookmark:
			have, ok := got[i].(*TextBookmark)
			if !ok || have.Type != want.Type || have.Text != want.Text {
				t.Fatalf("expected bookmark %d to be %#v, got %#v", i, want, got[i])
			}
		case *LinkBookmark:
			have, ok := got[i].(*LinkBookmark)
			if !ok || have.Type != want.Type || have.URL != want.URL {
				t.Fatalf("expected bookmark %d to be %#v, got %#v", i, want, got[i])
			}
		default:
			t.Fatalf("unexpected expected bookmark type %T", expected[i])
		}
	}
}
