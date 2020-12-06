// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
//
// Telegram related interactions.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
)

// Note that BotToken comes from a file not in a git repository.
// TODO: should be in the main function. For now hard and var for e2e testing.
var telegramApiPrefix = "https://api.telegram.org/bot" + BotToken

func methodURL(m string) string {
	return telegramApiPrefix + "/" + m
}

// NOTE: For not inlined keyborad see:
// https://core.telegram.org/bots/api/#replykeyboardmarkup
// Keep in mind that it's only possible to update (edit) message without
// reply_markup or with inline keyboards only. (Probably meaning that it's not
// possible to change reply_markup in editMessageText (apart from inlined
// keyboard).
type Message struct {
	Id       int64           `json:"message_id"`
	Text     string          `json:"text"`
	Entities json.RawMessage `json:"entities,omitempty"`
	Chat     struct {
		Id int64 `json:"id"`
	} `json:"chat"`
	ReplyMarkup interface{} `json:"reply_markup"`
}

type CallbackQuery struct {
	Id      string   `json:"id"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateId      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

func (u *Update) ChatId() (int64, error) {
	if u.Message != nil {
		return u.Message.Chat.Id, nil
	}
	if u.CallbackQuery != nil {
		return u.CallbackQuery.Message.Chat.Id, nil
	}
	return 0, fmt.Errorf("INTERNAL: Unimplemented ChatId() for Update\n%v", u)
}

type InlineKeyboard struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]*InlineKeyboard `json:"inline_keyboard"`
}

// https://core.telegram.org/bots/api#keyboardbutton
type ReplyKeyboardButton struct {
	Text            string `json:"text"`
	RequestLocation bool   `json:"request_location,omitempty"`
}

type ReplyKeyboardMarkup struct {
	Keyboard [][]*ReplyKeyboardButton `json:"keyboard"`
	Resize   bool                     `json:"resize_keyboard,omitempty"`
	OneTime  bool                     `json:"one_time_keyboard,omitempty"`
}

type MessageReply struct {
	ChatId      int64           `json:"chat_id"`
	Text        string          `json:"text"`
	Entities    json.RawMessage `json:"entities,omitempty"`
	ReplyMarkup interface{}     `json:"reply_markup,omitempty"`
	ParseMode   string          `json:"parse_mode,omitempty"`
}

type EditMessageText struct {
	ChatId      int64           `json:"chat_id"`
	MessageId   int64           `json:"message_id"`
	ParseMode   string          `json:"parse_mode,omitempty"`
	Text        string          `json:"text,omitempty"`
	Entities    json.RawMessage `json:"entities,omitempty"`
	ReplyMarkup interface{}     `json:"reply_markup,omitempty"`
}

type Telegram struct {
	hc         http.Client
	pollOffset int64
}

func (t *Telegram) Call(method string, req, res interface{}) error {
	log.Printf("Calling %q with req %v", method, req)
	mq, err := json.Marshal(req)
	if err != nil {
		return err
	}
	r, err := t.hc.Post(methodURL(method), "application/json", bytes.NewBuffer(mq))
	if err != nil {
		return err
	}
	return t.callHandleResponse(r, res)
}

func (t *Telegram) callHandleResponse(r *http.Response, res interface{}) error {
	b := new(bytes.Buffer)
	if _, err := b.ReadFrom(r.Body); err != nil {
		return err
	}
	if r.StatusCode != 200 {
		return fmt.Errorf("unexpected status code: got %d, want 200; %s", r.StatusCode, b.String())
	}

	log.Printf("DEBUG: body: %s", b.String())
	raw := new(struct {
		Ok     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	})
	if err := json.Unmarshal(b.Bytes(), raw); err != nil {
		return fmt.Errorf("Unmarshal(%q): %w", b.String(), err)
	}
	if !raw.Ok {
		return fmt.Errorf("got !ok %q", b.String())
	}

	return json.Unmarshal(raw.Result, res)
}

func (t *Telegram) Poll() (updates []*Update, err error) {
	if err = t.Call("getUpdates", &map[string]interface{}{
		"offset":  t.pollOffset,
		"timeout": 0,
	}, &updates); err != nil {
		return
	}
	for _, u := range updates {
		if u.UpdateId >= t.pollOffset {
			t.pollOffset = u.UpdateId + 1
		}
	}
	return
}

func (t *Telegram) SendTextMessage(chatId int64, s string) error {
	var m Message
	return t.Call("sendMessage", &MessageReply{
		ChatId: chatId,
		Text:   s,
	}, &m)
}

func (t *Telegram) SendMessage(mr *MessageReply) error {
	var m Message
	return t.Call("sendMessage", mr, &m)
}

func (t *Telegram) AnswerCallback(id string, text string) error {
	q := &struct {
		Id string `json:"callback_query_id"`
		T  string `json:"text,omitempty"`
		A  bool   `json:"show_alert"`
	}{
		Id: id,
		T:  text,
		A:  false,
	}
	var r bool
	if err := t.Call("answerCallbackQuery", q, &r); err != nil {
		return err
	}
	if !r {
		return errors.New("got false, want true")
	}
	return nil
}

func (t *Telegram) AnswerCallbackLog(id string, text string) {
	if err := t.AnswerCallback(id, text); err != nil {
		log.Printf("Error answering callback: %w", err)
	}
}

func (t *Telegram) SetWebhook(url string, certPath string) error {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	if certPath != "" {
		cert, err := os.Open(certPath)
		if err != nil {
			return err
		}
		defer cert.Close()
		fw, err := w.CreateFormFile("certificate", cert.Name())
		if err != nil {
			return err
		}
		if _, err := io.Copy(fw, cert); err != nil {
			return err
		}
	}
	if err := w.WriteField("url", url); err != nil {
		return err
	}
	// TODO: Support more than 1 connection. 1 for now because not everything
	// is safe for concurrent access.
	if err := w.WriteField("max_connections", "1"); err != nil {
		return err
	}
	w.Close()

	req, err := http.NewRequest("POST", methodURL("setWebhook"), &b)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	res, err := t.hc.Do(req)
	if err != nil {
		return err
	}
	var success bool
	if err := t.callHandleResponse(res, &success); err != nil {
		return err
	}
	if !success {
		return fmt.Errorf("Setting webhook was unsuccessful!")
	}
	return nil
}

func (t *Telegram) LogWebhookInfo() {
	raw := json.RawMessage{}
	if err := t.Call("getWebhookInfo", nil, &raw); err != nil {
		log.Printf("getWebhhokInfo failed: %v", err)
	}
	log.Printf("getWebhhokInfo: %s", string(raw))
}
