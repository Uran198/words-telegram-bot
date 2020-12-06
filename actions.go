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
// actions should contain action implementation for different actions that
// users can take.
package main

import (
	"encoding/json"
	"fmt"
)

// TODO: I am not sure if this is the best decision to bundle all up together.
// All objects needed to perform actions.
type Clients struct {
	Telegram    *Telegram
	Definer     *Definer
	Repetitions *Repetition
	Settings    *SettingsConfig
}

// TODO: Can I not extract word from the message? m.Text?
func flipWordCard(c *Clients, word string, m *Message, ks []*InlineKeyboard) error {
	// TODO: It isn't always neccessary to retrieve defitnion when this
	// function is used.
	def, entities, err := c.Repetitions.GetDefinition(m.Chat.Id, word)
	if err != nil {
		return fmt.Errorf("retrieving definition: %v", err)
	}
	if ks == nil {
		ks = []*InlineKeyboard{}
	}
	r := &EditMessageText{
		ChatId:    m.Chat.Id,
		MessageId: m.Id,
		// TODO: Enable replying in markdown, but for that need to store
		// definitions escaped.
		//ParseMode:   "MarkdownV2",
		Text:     def,
		Entities: json.RawMessage(entities),
		// FIXME: Should InlineKeyboard be refactored for less duplication?
		ReplyMarkup: &InlineKeyboardMarkup{
			InlineKeyboard: [][]*InlineKeyboard{ks},
		},
	}
	var rm Message
	if err := c.Telegram.Call("editMessageText", r, &rm); err != nil {
		return fmt.Errorf("editing message: %w", err)
	}
	return nil
}
