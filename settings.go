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
	"encoding/json"
	"fmt"
)

type Settings struct {
	// Language of the input words.
	InputLanguage string
	// FIXME: Maybe there is a library to convert?
	// InputLanguageISO639_3 is an ISO 639-3 language code for the language in which to
	// extract examples.
	InputLanguageISO639_3 string
	// TranslationLanguages is an ISO 639-3 language codes for the language into
	// which to accept the translations.
	// true if translation is accepted
	TranslationLanguages map[string]bool
	TimeZone             string
}

func SettingsFromString(s string) *Settings {
	var m Settings
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		panic(err)
	}
	return &m
}

func DefaultSettings() *Settings {
	return &Settings{
		InputLanguage:         "Hungarian",
		InputLanguageISO639_3: "hun",
		TranslationLanguages: map[string]bool{
			"eng": true,
			"rus": true,
			"ukr": true,
		},
		TimeZone: "UTC",
	}
}

func (s Settings) String() string {
	m, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(m)
}

type SettingsConfig struct {
	db *sql.DB
}

func NewSettingsConfig(dbPath string) (*SettingsConfig, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS Settings (
			chat_id INTEGER PRIMARY KEY,
			settings STRING -- json serialized settings
		);`); err != nil {
		return nil, err
	}
	return &SettingsConfig{db}, nil
}

func (c *SettingsConfig) GetAll() (map[int64]*Settings, error) {
	rows, err := c.db.Query(`
		SELECT chat_id, settings
		FROM Settings`)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	r := make(map[int64]*Settings)

	for rows.Next() {
		var (
			chatID int64
			s      string
		)
		if err := rows.Scan(&chatID, &s); err != nil {
			return nil, err
		}
		r[chatID] = SettingsFromString(s)
	}
	return r, nil
}

func (c *SettingsConfig) Get(chatID int64) (*Settings, error) {
	row := c.db.QueryRow(`
		SELECT settings
		FROM Settings
		WHERE chat_id = $0`,
		chatID)
	var s string
	if err := row.Scan(&s); err != nil {
		if err == sql.ErrNoRows {
			return DefaultSettings(), nil
		}
		return nil, fmt.Errorf("INTERNAL: retrieving settings for chat id %d: %w", chatID, err)
	}
	return SettingsFromString(s), nil
}

func (c *SettingsConfig) Set(chatID int64, s *Settings) error {
	_, err := c.db.Exec(`
		INSERT OR REPLACE INTO Settings(chat_id, settings) VALUES
		($0, $1);`,
		chatID, s.String())
	if err != nil {
		return fmt.Errorf("INTERNAL: Failed updating settings: %w", err)
	}
	return nil
}

func (c *SettingsConfig) ValidateLanguage(language string) error {
	_, ok := SupportedInputLanguages[language]
	if !ok {
		return fmt.Errorf("unsupported language %q", language)
	}
	return nil
}

func (c *SettingsConfig) SetLanguage(chatid int64, language string) error {
	currentSettings, err := c.Get(chatid)
	if err != nil {
		return err
	}
	if err := c.ValidateLanguage(language); err != nil {
		return err
	}
	languageSettings := SupportedInputLanguages[language]
	currentSettings.InputLanguage = languageSettings.InputLanguage
	currentSettings.InputLanguageISO639_3 = languageSettings.InputLanguageISO639_3
	currentSettings.TranslationLanguages = languageSettings.TranslationLanguages
	return c.Set(chatid, currentSettings)
}

func (c *SettingsConfig) ValidateTimeZone(tz string) error {
	set := TimeZones[tz]
	if !set {
		return fmt.Errorf("unsupported time zone (format should be UTC, UTC+X or UTC-X)")
	}
	return nil
}

func (c *SettingsConfig) SetTimeZone(chatid int64, tz string) error {
	if err := c.ValidateTimeZone(tz); err != nil {
		return err
	}
	currentSettings, err := c.Get(chatid)
	if err != nil {
		return err
	}
	currentSettings.TimeZone = tz
	return c.Set(chatid, currentSettings)
}
