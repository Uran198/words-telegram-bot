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
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Need to implement
// /save custom card for practice
// /modify

type PracticeType int

const (
	// answer should match exact word
	// FIXME: FIXME: FIXME: FIXME: This doesn't work!!!!!!!!
	//  cannot save obfuscated - cannot check.
	//  cannot save clear - cannot extract raw from obfuscated.
	//  this need fixing - make sure repetition_test passes.
	PracticeWordSpelling PracticeType = iota
	// answer of the form know/don't know
	PracticeKnowledge
)

const UsePractice = PracticeKnowledge

var (
	SupportedInputLanguages = map[string]Settings{
		"Hungarian": Settings{
			InputLanguage:         "Hungarian",
			InputLanguageISO639_3: "hun",
			TranslationLanguages: map[string]bool{
				"eng": true,
				"rus": true,
				"ukr": true,
			},
		},
		"English": Settings{
			InputLanguage:         "English",
			InputLanguageISO639_3: "eng",
			TranslationLanguages: map[string]bool{
				"rus": true,
				"ukr": true,
			},
		},
		"German": Settings{
			InputLanguage:         "German",
			InputLanguageISO639_3: "deu",
			TranslationLanguages: map[string]bool{
				"eng": true,
				"rus": true,
				"ukr": true,
			},
		},
	}
	TimeZones = func() map[string]bool {
		timeZones := make(map[string]bool)
		for i := -12; i < 12; i++ {
			timeZones[fmt.Sprintf("UTC%+d", i)] = true
		}
		timeZones["UTC"] = true
		return timeZones
	}()
)

type CallbackAction int

const (
	SaveWordAction CallbackAction = iota
	PracticeKnowAction
	PracticeDontKnowAction
	ResetProgressAction
	PracticeAnswerAction
	ShowAnswerAction
)

// Make sure all fields are Public, otherwise encoding will not work
// NOTE: Maximum size of this struct when json-encoded in 64 bytes.
// TODO: Should include ID to make sure the same action is not performed many
// times? In general should keep track of different IDs to make sure that stuff
// is not processed more than once?
type CallbackInfo struct {
	Action CallbackAction
	// Not every field below will be set for each action.
	WordID int64
	Ease   AnswerEase
}

// FIXME: Should return an error!
func CallbackInfoFromString(s string) CallbackInfo {
	var c CallbackInfo
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		panic(err)
	}
	return c
}

func (c CallbackInfo) String() string {
	m, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	return string(m)
}

type Commander struct {
	*Clients
	bot *Bot
}

type CommanderOptions struct {
	againDelay     time.Duration
	dbPath         string
	port           int
	certPath       string
	keyPath        string
	ip             string
	push           bool
	stages         []time.Duration
	wordsCacheSize int
}

func escapeMarkdown(s string) string {
	r := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return r.Replace(s)
}

func NewCommander(tm *Telegram, opts *CommanderOptions) (*Commander, error) {
	hc := &http.Client{}
	// TODO: Can use errgroup if there is a need to paralelize. This is the
	// slowest step in initialization.
	uf, err := NewUsageFetcher(opts.dbPath)
	if err != nil {
		return nil, fmt.Errorf("creating usage fetcher: %w", err)
	}
	sc, err := NewSettingsConfig(opts.dbPath)
	if err != nil {
		return nil, fmt.Errorf("creating settings config: %w", err)
	}
	d := &Definer{
		usage: uf,
		http:  hc,
	}
	r, err := NewRepetition(opts.dbPath, opts.stages)
	if err != nil {
		return nil, err
	}
	r.againDelay = opts.againDelay
	c := &Clients{
		Telegram:    tm,
		Definer:     d,
		Repetitions: r,
		Settings:    sc,
	}

	// Make sure that telegram client is setup correctly
	raw := json.RawMessage{}
	if err := tm.Call("getMe", nil, &raw); err != nil {
		return nil, err
	}
	log.Printf("getMe: %s", string(raw))

	return &Commander{
		Clients: c,
		bot: &Bot{
			state:   &State{c, NewWordsCache(opts.wordsCacheSize)},
			command: make(map[int64]Command),
		},
	}, nil
}

// Update processes the user's update and spit out output.
// Should return an error only on unrecoverable errors due to which we cannot
// continue execution.
// TODO: Use answerCallbackQuery to notify client that callback was processed?
func (c *Commander) Update(u *Update) error {
	err := c.bot.Update(u)
	if err != nil {
		// Not sure what to do otherwise, but crashing isn't nice.
		log.Printf("INTENAL ERROR for update %v: %v", u, err)
	}
	return nil
}

func (c *Commander) PollAndProcess() error {
	// TODO: Push instead of Poll
	updates, err := c.Telegram.Poll()
	if err != nil {
		return err
	}
	log.Printf("updates: %v", updates)
	if len(updates) > 0 {
		log.Printf("sample message: %v", updates[0].Message)
	}
	// query:
	// for definitions: https://ertelmezo.oszk.hu/kereses.php?kereses=dal
	// for wiktionary:
	// for translation:
	// Don't display more than x for each source.
	// After that:
	// memoization (ask questions and check prob show definition & then check), storage (start with something simple to use word -> definition).

	for _, u := range updates {
		if err := c.Update(u); err != nil {
			return err
		}
	}
	return nil
}

func (c *Commander) handleUpdate(req *http.Request) error {
	if req.Method != "POST" {
		return fmt.Errorf("want POST; got %s", req.Method)
	}
	b := new(bytes.Buffer)
	if _, err := b.ReadFrom(req.Body); err != nil {
		return fmt.Errorf("reading body of req %v: %v", req, err)
	}
	var update Update
	if err := json.Unmarshal(b.Bytes(), &update); err != nil {
		return fmt.Errorf("json.Unmarshal(%q): %w", b.String(), err)
	}
	return c.Update(&update)
}

func (c *Commander) WebhookCallback(w http.ResponseWriter, req *http.Request) {
	if err := c.handleUpdate(req); err != nil {
		log.Printf("INTERNAL ERROR[Webhook]: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
	}
	w.WriteHeader(http.StatusOK)
}

func (c *Commander) StartPush(opts *CommanderOptions) error {
	addr := fmt.Sprintf("https://%s:%d/%s", opts.ip, opts.port, BotToken)
	if err := c.Telegram.SetWebhook(addr, opts.certPath); err != nil {
		return err
	}
	c.Telegram.LogWebhookInfo()
	mux := http.NewServeMux()
	mux.HandleFunc("/"+BotToken, c.WebhookCallback)
	cfg := &tls.Config{
		MinVersion:               tls.VersionTLS12,
		CurvePreferences:         []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256},
		PreferServerCipherSuites: true,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		},
	}
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", opts.port),
		Handler:      mux,
		TLSConfig:    cfg,
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler), 0),
	}
	log.Printf("Starting serving on %s", addr)
	return srv.ListenAndServeTLS(opts.certPath, opts.keyPath)
}

// TODO: Accept time.Ticker channel -> Will give an ability to inline
// PollAndProcess and test Start in addition to the rest.
func (c *Commander) StartPoll() error {
	// Reset webhook, otherwise getUpdates would not work!
	if err := c.Telegram.SetWebhook("", ""); err != nil {
		return err
	}
	c.Telegram.LogWebhookInfo()
	for {
		if err := c.PollAndProcess(); err != nil {
			return err
		}
		time.Sleep(time.Second * 3)
	}
}
