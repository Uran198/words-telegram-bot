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
	PracticeDontKnowActionNoPractice
)

// Make sure all fields are Public, otherwise encoding will not work
// TODO: Should include ID to make sure the same action is not performed many
// times? In general should keep track of different IDs to make sure that stuff
// is not processed more than once?
type CallbackInfo struct {
	Action CallbackAction
	// One of below is set depending on the action.
	Word    string
	Setting string
}

// FIXME: Should return an error?
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
	useCache bool
	dbPath   string
	stages   []time.Duration
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
	var cache DefCacheInterface
	if opts.useCache {
		var err error
		cache, err = NewDefCache(opts.dbPath)
		if err != nil {
			return nil, fmt.Errorf("new cache(%q): %w", opts.dbPath, err)
		}
	} else {
		cache = &NoCache{}
	}
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
		cache: cache,
		http:  hc,
	}
	r, err := NewRepetition(opts.dbPath, opts.stages)
	if err != nil {
		return nil, err
	}
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
			state:   &State{c},
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

// TODO: Accept time.Ticker channel -> Will give an ability to inline
// PollAndProcess and test Start in addition to the rest.
func (c *Commander) Start() error {
	for {
		if err := c.PollAndProcess(); err != nil {
			return err
		}
		time.Sleep(time.Second * 3)
	}
}
