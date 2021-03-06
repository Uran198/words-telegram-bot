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
	"database/sql"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestReminders(t *testing.T) {
	dir, err := ioutil.TempDir("", "repetition")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Temp dir: %q", dir)
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "tmpdb")

	settings, err := NewSettingsConfig(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	r, err := NewReminder(&Clients{
		Settings: settings,
	}, db)
	if err != nil {
		t.Fatal(err)
	}

	const chatID int64 = 0
	if err := settings.Set(chatID, DefaultSettings()); err != nil {
		t.Fatal(err)
	}

	c := make(chan time.Time)

	cancel := make(chan struct{})
	go func() {
		c <- time.Now()
		cancel <- struct{}{}
	}()

	var sent []*Notification
	r.sendNofication = func(n *Notification) error {
		sent = append(sent, n)
		return nil
	}

	r.Loop(c, cancel)

	if len(sent) != 1 {
		t.Errorf("got %d notifications (%v), want 1", len(sent), sent)
	}
}
