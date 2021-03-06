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
	"fmt"
	"log"
	"time"
)

type Notification struct {
	ChatID int64
}

// reminder
type Reminder struct {
	sendNofication func(*Notification) error
	fetchSettings  func() (map[int64]*Settings, error)

	// db stores last reminder time for each chat ID.
	db *sql.DB
}

func NewReminder(c *Clients, db *sql.DB) (*Reminder, error) {

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS Reminders (
			chat_id INTEGER PRIMARY KEY,
			last_reminder_time_seconds INTEGER -- seconds since UNIX epoch
		);`); err != nil {
		return nil, err
	}

	return &Reminder{
		db: db,
		sendNofication: func(n *Notification) error {
			log.Printf("Notification: %v", n)
			return nil
		},
		fetchSettings: c.Settings.GetAll,
	}, nil
}

func (r *Reminder) LastReminderTime(chatID int64) (time.Time, error) {
	row := r.db.QueryRow(`
		SELECT last_reminder_time_seconds
		FROM Reminders
		WHERE chat_id = $0`,
		chatID)
	var u int64
	err := row.Scan(&u)
	if err != nil {
		u = 0
		if err != sql.ErrNoRows {
			err = fmt.Errorf("INTERNAL: retrieving last_reminder_time_seconds for chat id %d: %w", chatID, err)
		}
	}
	return time.Unix(u, 0), err
}

func (r *Reminder) UpdateLastReminderTime(chatID int64) error {
	_, err := r.db.Exec(`
		INSERT OR REPLACE INTO Reminders(chat_id, last_reminder_time_seconds) VALUES
		($0, $1);`,
		chatID, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("INTERNAL: Failed updating reminder_time: %w", err)
	}
	return nil
}

func (r *Reminder) Loop(ticker <-chan time.Time, cancel <-chan struct{}) {
	for {
		cs, err := r.fetchSettings()
		if err != nil {
			log.Printf("ERROR: fetchSettings: %v", err)
		}
		// TODO: Take into account availability window, reminder frequency and timezone.
		// newReminderTime = lastReminder + (aval window size)/Frequency
		for chatID, _ := range cs {
			const frequency = 1
			rt, err := r.LastReminderTime(chatID)
			if err != nil {
				log.Print(err)
			}
			newRT := rt.Add(24 / frequency * time.Hour)
			if time.Now().After(newRT) {
				if err := r.sendNofication(&Notification{chatID}); err != nil {
					log.Print(err)
				}
				if err := r.UpdateLastReminderTime(chatID); err != nil {
					log.Print(err)
				}
			}
		}
		select {
		case <-ticker:
		case <-cancel:
			return
		}
	}
}
