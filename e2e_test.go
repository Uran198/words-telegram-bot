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
// Provides e2e test scenario testing faking external dependencies.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TODO: have a fake wiki server.
func TestTelegramBotE2E(t *testing.T) {
	// TODO: mock wiki
	// TODO: test edit message
	type Test struct {
		// if prefixed with b: button is pressed
		Send        string
		Want        string
		WantButtons []string
	}

	// Order is important!
	send := strings.Split(strings.Trim(`
/start

many words

oijasdki#noresults#

/practice

fekete

fekete

b:Learn

fekete

/practice

b:Don't know

/settings

/practice

b:Don't know

b:Know

falu

b:Learn

/practice

b:Don't know

/stop

/settings

b:Input Language

NotARealLanguage

English

/stop

/settings

b:Input Language

Hungarian

b:Input Language

/stop

/delete falu

/practice

/add

/stop

/add

cardfront
cardback (definitions or what not)

cardfront

/practice
`, "\n"), "\n\n")

	dir, err := ioutil.TempDir("", "e2e")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	db := filepath.Join(dir, "tmpdb")

	fk := startFakeTelegram(t)
	defer fk.server.Close()
	tm := &Telegram{hc: *fk.server.Client()}

	c, err := NewCommander(tm, &CommanderOptions{
		useCache:      true,
		dbPath:        db,
		sentencesPath: "./testdata/sentences.csv",
		linksPath:     "./testdata/links.csv",
		stages: []time.Duration{
			0,
			2 * time.Minute,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var got []Test
	for _, msg := range send {
		t.Logf("msg: %s", msg)
		if strings.HasPrefix(msg, "b:") {
			if err := fk.PressButton(strings.TrimPrefix(msg, "b:")); err != nil {
				t.Error(err)
			}
		} else {
			fk.SendMessage(msg)
		}
		if err := c.PollAndProcess(); err != nil {
			t.Fatal(err)
		}
		lm := fk.messages[len(fk.messages)-1]
		var bs []string
		for _, ks := range lm.ReplyMarkup.InlineKeyboard {
			for _, k := range ks {
				bs = append(bs, k.Text)
			}
		}
		got = append(got, Test{
			Send:        msg,
			Want:        lm.Text,
			WantButtons: bs,
		})
	}
	b, err := ioutil.ReadFile(path.Join("testdata", "e2e.json"))
	if err != nil {
		t.Error(err)
	}
	var want []Test
	if err := json.Unmarshal(b, &want); err != nil {
		t.Error(err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Error("got != want")
	}

	cf := path.Join("testdata", "e2e_corrected.json")
	if t.Failed() {
		t.Logf("Saving corrected test data to %s", cf)
		m, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := ioutil.WriteFile(cf, m, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func startFakeTelegram(t *testing.T) *fakeTelegram {
	marshal := func(i interface{}) []byte {
		raw := struct {
			Ok     bool        `json:"ok"`
			Result interface{} `json:"result"`
		}{
			Ok:     true,
			Result: i,
		}
		msg, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		return msg
	}
	fk := &fakeTelegram{}
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := strings.TrimLeft(r.URL.Path, "/")
		switch m {
		case "getUpdates":
			w.Write(marshal(fk.updates))
			fk.updates = nil
		case "editMessageText", "editMessageReplyMarkup":
			b := new(bytes.Buffer)
			if _, err := b.ReadFrom(r.Body); err != nil {
				t.Fatal(err)
			}
			var m EditMessageText
			if err := json.Unmarshal(b.Bytes(), &m); err != nil {
				t.Fatal(err)
			}
			// TODO: To make tests more realistic might make sense to give
			// messages ids and respect them here.
			// assume that edits are always for the last message
			lm := fk.messages[len(fk.messages)-1]
			lm.Text = m.Text
			lm.ReplyMarkup = m.ReplyMarkup
			w.Write(marshal(lm))
		case "getMe":
			w.Write(marshal("getMe was called. This is fake telegram."))
		case "sendMessage":
			b := new(bytes.Buffer)
			if _, err := b.ReadFrom(r.Body); err != nil {
				t.Fatal(err)
			}
			var m Message
			if err := json.Unmarshal(b.Bytes(), &m); err != nil {
				t.Fatal(err)
			}
			fk.messages = append(fk.messages, &m)
			w.Write(marshal(m))
		}
	}))
	fk.server = s
	telegramApiPrefix = s.URL
	return fk
}

type fakeTelegram struct {
	server *httptest.Server
	// all the messages ever received
	messages []*Message
	// updates to return on the call to "getUpdates"
	updates []Update
}

func (fk *fakeTelegram) SendMessage(s string) {
	fk.updates = append(fk.updates, Update{
		UpdateId: 0,
		Message: &Message{
			Id:   0,
			Text: s,
			Chat: struct {
				Id int64 `json:"id"`
			}{
				Id: 0,
			},
		},
	})
}

func (fk *fakeTelegram) PressButton(button string) error {
	lm := fk.messages[len(fk.messages)-1]
	for _, ks := range lm.ReplyMarkup.InlineKeyboard {
		for _, k := range ks {
			if k.Text == button {
				fk.updates = append(fk.updates, Update{
					UpdateId: 0,
					CallbackQuery: &CallbackQuery{
						Id:      "0",
						Message: lm,
						Data:    k.CallbackData,
					},
				})
				return nil
			}
		}
	}
	return fmt.Errorf("No button found %q", button)
}
