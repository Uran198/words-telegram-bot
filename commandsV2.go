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
// commandsV2 is a replacement for action-based message processing. Everything
// inside here is responsible for tying database together with telegram
// interactions.
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
)

type Callback interface {
	Call(*State, *CallbackQuery) error
}

type State struct {
	*Clients
	Cache *WordsCache
}

// TODO:
func (s *State) LoadCommand(chatID int64) (*SerializedCommand, error) {
	return nil, nil
}

// TODO:
func (s *State) SaveCommand(chatID int64, _ *SerializedCommand) error {
	return nil
}

type Bot struct {
	state   *State
	command map[int64]Command
}

func (b *Bot) fetchCommand(chatID int64) Command {
	cmd := b.command[chatID]
	if cmd == nil {
		s, err := b.state.LoadCommand(chatID)
		if err == nil {
			cmd, err = s.AsCommand()
		}
		if err != nil {
			cmd = nil
			log.Printf("INTERNAL ERROR: couldn't fetch command for chat %d: %v", chatID, err)
		}
		if cmd == nil {
			cmd = CommandsTemplate.DefaultCommand("")
		}
	}
	b.command[chatID] = cmd
	return cmd
}

func (b *Bot) updateCommand(chatID int64, cmd Command) error {
	b.command[chatID] = cmd
	var s *SerializedCommand
	if cmd != nil {
		s = cmd.Serialize()
	}
	return b.state.SaveCommand(chatID, s)
}

func (b *Bot) Update(u *Update) (err error) {
	chatId, _ := u.ChatId()
	if err != nil {
		return err
	}

	// Try surfacing UserError and update the bot accordingly on internal error.
	defer func() {
		var e UserError
		if errors.As(err, &e) {
			err = e.Surface(b.state)
		}
		if err == nil {
			return
		}
		log.Printf("INTERNAL ERROR: %v", err)
		// Reset command so that user wouldn't be stuck with internal errors
		// with no way out.
		if upErr := b.updateCommand(chatId, nil); upErr != nil {
			log.Printf("INTERNAL ERROR: updateCommand(%d, nil): %v; while handling %v", chatId, upErr, err)
		}
	}()

	if u.CallbackQuery != nil {
		var c CallbackInfo
		// Make sure that we can unmarshall, so that we don't panic accidentally later on.
		if err = json.Unmarshal([]byte(u.CallbackQuery.Data), &c); err != nil {
			return fmt.Errorf("Failed to unmarshal callback query %s: %w", u.CallbackQuery.Data, err)
		}
		return CommandsTemplate.Callback[c.Action].Call(b.state, u.CallbackQuery)
	}

	if u.Message == nil {
		// TODO: Make internal error type.
		return fmt.Errorf("INTERNAL ERROR: Update is neither a message, nor a callback query: %v", u)
	}

	// Update is a Message.
	msg := u.Message.Text
	for n, f := range CommandsTemplate.Commands {
		if msg == n {
			cmd := f(n)
			cmd, err = cmd.OnCommand(b.state, u.Message)
			if err != nil {
				return err
			}
			if cmd == nil {
				cmd = CommandsTemplate.DefaultCommand("")
			}
			return b.updateCommand(chatId, cmd)
		}
	}

	// None of the commands match, so process the message.
	cmd := b.fetchCommand(chatId)
	cmd, err = cmd.ProcessMessage(b.state, u.Message)
	// On user caused error command should still be updated accordingly.
	if err == nil || errors.Is(err, UserError{}) {
		return b.updateCommand(chatId, cmd)
	}
	return err
}

type SerializedCommand struct {
	// Name of the command
	Name string
	// Serialized command's state. Command should be able to restore it's state
	// from this string.
	Data []byte
}

func (s *SerializedCommand) AsCommand() (Command, error) {
	factory := CommandsTemplate.DefaultCommand
	if s == nil {
		return factory(""), nil
	}
	for n, f := range CommandsTemplate.Commands {
		if n == s.Name {
			factory = f
		}
	}
	cmd := factory(s.Name)
	return cmd, cmd.Init(s)
}

type Command interface {
	Serialize() *SerializedCommand
	Init(*SerializedCommand) error
	// Process message from the user.
	ProcessMessage(*State, *Message) (Command, error)
	// Called when user runs this command.
	OnCommand(*State, *Message) (Command, error)
	// Both OnCommand and ProcessMessage return the new context command
}

// Factory is used so that Command metadata is not changed accidentally.
type CommandFactory func(name string) Command

// UserError is an error that should be surfaced to the user.
type UserError struct {
	ChatID int64
	Err    error
}

func (u UserError) Error() string {
	return u.Err.Error()
}

// Surface surfaces error to the user.
func (u UserError) Surface(s *State) error {
	e := u.Error()
	if len(e) > 0 {
		e = strings.ToUpper(e[:1]) + e[1:]
	}
	return s.Telegram.SendTextMessage(u.ChatID, e)
}

type question struct {
	name     string
	ask      func(s *State, chatID int64) error
	validate func(*State, *Message) error
	answer   string
}

type multiQuestionCommand struct {
	name string
	// All questions should have unique names.
	// range is used instead of map so that the called can control questions' order.
	questions []*question
	// Once user answers all questions save will be called. questions will have
	// answers populated.
	save func(state *State, chatID int64, questions []*question) error
	// Name of the last question we have asked.
	lastQuestion string
}

func MultiQuestionCommandFactory(questions []*question, save func(state *State, chatID int64, questions []*question) error) CommandFactory {
	return func(name string) Command {
		// We need to copy questions to avoid reusing the same questions for
		// multiple instances.
		cq := make([]*question, len(questions))
		for i, q := range questions {
			cq[i] = new(question)
			*cq[i] = *q
		}
		return &multiQuestionCommand{
			name:      name,
			questions: cq,
			save:      save,
		}
	}
}

type multiQuestionCommandSerialized struct {
	answers      map[string]string
	lastQuestion string
}

func (c *multiQuestionCommand) Serialize() *SerializedCommand {
	// No need to serialize question names, they should be the same in CommandsTemplate.
	// Need to serialize answers to the questions though.
	a := make(map[string]string)
	for _, q := range c.questions {
		a[q.name] = q.answer
	}
	cs := &multiQuestionCommandSerialized{
		answers:      a,
		lastQuestion: c.lastQuestion,
	}
	b, err := json.Marshal(cs)
	if err != nil {
		log.Printf("INTERNAL ERROR: Couldn't serialize %v: %v", cs, err)
	}
	return &SerializedCommand{
		Name: c.name,
		Data: b,
	}
}

func (c *multiQuestionCommand) Init(s *SerializedCommand) error {
	cs := &multiQuestionCommandSerialized{}
	if err := json.Unmarshal(s.Data, &cs); err != nil {
		return fmt.Errorf("Unmarshal(%s): %w", s.Data, err)
	}
	for _, q := range c.questions {
		q.answer = cs.answers[q.name]
	}
	c.lastQuestion = cs.lastQuestion
	return nil
}

func (c *multiQuestionCommand) OnCommand(s *State, m *Message) (Command, error) {
	chatID := m.Chat.Id
	if len(c.questions) == 0 {
		// With to questions, no reason to have this command process messages.
		return nil, c.save(s, chatID, c.questions)
	}
	if err := c.questions[0].ask(s, chatID); err != nil {
		return nil, err
	}
	c.lastQuestion = c.questions[0].name
	return c, nil
}

func (c *multiQuestionCommand) ProcessMessage(s *State, m *Message) (Command, error) {
	var q *question
	for _, qe := range c.questions {
		if qe.name == c.lastQuestion {
			q = qe
			break
		}
	}
	if q == nil {
		return nil, fmt.Errorf("INTERNAL ERROR: Did not find a question corresponding to last question %s", c.lastQuestion)
	}
	if err := q.validate(s, m); err != nil {
		// In case validate fails with user error, we want to be able to retry,
		// so we return c to be a new command.
		return c, err
	}
	q.answer = m.Text

	var next *question = nil
	for _, qe := range c.questions {
		if qe.answer == "" {
			next = qe
			break
		}
	}
	if next == nil {
		err := c.save(s, m.Chat.Id, c.questions)
		// After all questions have been answered there is no point in
		// keeping trying to save, even if it fails with UserError.
		return nil, err
	}
	if err := next.ask(s, m.Chat.Id); err != nil {
		// ask should never fail with user error.
		return nil, err
	}
	c.lastQuestion = next.name
	return c, nil
}
func ReplyCommand(reply func(s *State, chatID int64) error) CommandFactory {
	return MultiQuestionCommandFactory(
		nil,
		func(s *State, chatID int64, _ []*question) error {
			return reply(s, chatID)
		},
	)
}

func NewMessageReply(chatID int64, text, entities string, ik ...*InlineKeyboard) *MessageReply {
	var rm interface{}
	if len(ik) > 0 {
		rm = &InlineKeyboardMarkup{
			InlineKeyboard: [][]*InlineKeyboard{ik},
		}
	}
	return &MessageReply{
		ChatId:      chatID,
		Text:        text,
		Entities:    json.RawMessage(entities),
		ReplyMarkup: rm,
	}
}

// practiceReply sends practice card to the user.
func practiceReply(s *State, chatID int64) error {
	switch UsePractice {
	case PracticeKnowledge:
	default:
		panic(fmt.Sprintf("INTERNAL: Unimplemented practice type: %v", UsePractice))
	}
	word, err := s.Repetitions.RepeatWord(chatID)
	if err == sql.ErrNoRows {
		// FIXME: Make this user error instead.
		return s.Telegram.SendTextMessage(chatID, "No more rows to practice; exiting practice mode.")
	}
	if err != nil {
		return fmt.Errorf("retrieving word for repetition: %w", err)
	}
	id := s.Cache.Add(chatID, word)
	return s.Telegram.SendMessage(NewMessageReply(chatID, word, "", showAnswerIK(id)))
}

// settingsReply sends current settings and instructions on how to change them.
func settingsReply(state *State, chatID int64) error {
	s, err := state.Settings.Get(chatID)
	if err != nil {
		return err
	}
	var ls []string
	for l, v := range s.TranslationLanguages {
		if v {
			ls = append(ls, fmt.Sprintf("%q", l))
		}
	}
	sort.Strings(ls)
	var cmds []string
	for k, _ := range SettingsCommands {
		cmds = append(cmds, "  "+k)
	}
	sort.Strings(cmds)
	msg := fmt.Sprintf(`
Current settings:

Input language: %q
Input language in ISO 639-3: %q
Translation languages in ISO 639-3: %s
Time Zone: %s

To modify settings use one of the commands below:
%s
`, s.InputLanguage, s.InputLanguageISO639_3, strings.Join(ls, ","), s.TimeZone, strings.Join(cmds, "\n"))
	return state.Telegram.SendMessage(&MessageReply{
		ChatId: chatID,
		Text:   msg,
	})
}

// statsReply sends current stats to the user.
func statsReply(state *State, chatID int64) error {
	s, err := state.Repetitions.Stats(chatID)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("Number of words saved for learning: %d", s.WordCount)
	return state.Telegram.SendMessage(NewMessageReply(chatID, msg, ""))
}

// This inteface is a bit redundant. We need it though to avoid initialization
// loop with SettingsCommands depending on settingsReply and settingsReply
// depending on SettingsCommands.
type SimpleQuestionCommand interface {
	Ask(_ *State, chatID int64) error
	Validate(*State, *Message) error
	Save(_ *State, chatID int64, answer string) error
}

func SimpleQuestionCommandFactory(c SimpleQuestionCommand) CommandFactory {
	return MultiQuestionCommandFactory(
		[]*question{{
			name:     "question",
			ask:      c.Ask,
			validate: c.Validate,
		}},
		func(s *State, chatID int64, questions []*question) error {
			return c.Save(s, chatID, questions[0].answer)
		},
	)
}

type SimpleSettingCommand struct {
	question string
	validate func(s *State, answer string) error
	save     func(s *State, chatID int64, answer string) error
}

func (c *SimpleSettingCommand) Ask(s *State, chatID int64) error {
	return askQuestion(c.question)(s, chatID)
}

func (c *SimpleSettingCommand) Validate(s *State, m *Message) error {
	if err := c.validate(s, m.Text); err != nil {
		return UserError{ChatID: m.Chat.Id, Err: fmt.Errorf("%w. Please try again.", err)}
	}
	return nil
}

func (c *SimpleSettingCommand) Save(s *State, chatID int64, answer string) error {
	if err := c.save(s, chatID, answer); err != nil {
		return err
	}
	return settingsReply(s, chatID)
}

func askQuestion(q string) func(s *State, chatID int64) error {
	return func(s *State, chatID int64) error {
		return s.Telegram.SendTextMessage(chatID, q)
	}
}

func AddCommandFactory() CommandFactory {
	noopValidate := func(*State, *Message) error { return nil }
	return MultiQuestionCommandFactory(
		[]*question{{
			name:     "front",
			ask:      askQuestion("Enter front of the card (word, expression, question)."),
			validate: noopValidate,
		}, {
			name:     "back",
			ask:      askQuestion("Enter back of the card (definition, answer)."),
			validate: noopValidate,
		}},
		func(s *State, chatID int64, qs []*question) error {
			var front string
			var back string
			for _, q := range qs {
				switch q.name {
				case "front":
					front = q.answer
				case "back":
					back = q.answer
				default:
					return fmt.Errorf("unexpected question in save: %v", q)
				}
			}
			// FIXME: Preserve entities, so user's formatting will be saved.
			if err := s.Repetitions.Save(chatID, front, back, ""); err != nil {
				return err
			}
			return s.Telegram.SendTextMessage(chatID, fmt.Sprintf("Added %q for learning!", front))
		},
	)
}

func DeleteCommandFactory() CommandFactory {
	return MultiQuestionCommandFactory(
		[]*question{{
			name: "word",
			ask:  askQuestion("Enter the word you want to delete from learning!"),
			validate: func(s *State, m *Message) error {
				e, err := s.Repetitions.Exists(m.Chat.Id, m.Text)
				if err != nil {
					return err
				}
				if !e {
					return UserError{ChatID: m.Chat.Id, Err: fmt.Errorf("Word %q isn't saved for learning!", m.Text)}
				}
				return nil
			},
		}},
		func(s *State, chatID int64, qs []*question) error {
			if err := s.Repetitions.Delete(chatID, qs[0].answer); err != nil {
				return err
			}
			return s.Telegram.SendTextMessage(chatID, fmt.Sprintf("Deleted %q!", qs[0].answer))
		},
	)
}

type defaultCommand struct{}

func (defaultCommand) Serialize() *SerializedCommand {
	return nil
}
func (defaultCommand) Init(*SerializedCommand) error {
	return nil
}
func (defaultCommand) ProcessMessage(s *State, m *Message) (Command, error) {
	chatID := m.Chat.Id
	wordID := s.Cache.Add(chatID, m.Text)

	def, entities, err := s.Repetitions.GetDefinition(m.Chat.Id, m.Text)
	if err == nil {
		return nil, s.Telegram.SendMessage(NewMessageReply(
			m.Chat.Id, def, entities, resetProgressIK(wordID)))
	}
	if err != sql.ErrNoRows {
		log.Printf("ERROR: Repetitions(%d, %s): %v", m.Chat.Id, m.Text, err)
	}

	if len(strings.Split(m.Text, " ")) > 1 {
		return nil, UserError{ChatID: chatID, Err: fmt.Errorf("For now this bot doesn't work with expressions. Try entering a single work without spaces.")}
	}

	settings, err := s.Settings.Get(chatID)
	if err != nil {
		return nil, fmt.Errorf("get settings: %v", err)
	}
	ds, err := s.Definer.Define(m.Text, settings)
	if err != nil {
		// TODO: Might be good to post debug logs to the reply in the debug mode.
		log.Printf("Error fetching the definition: %w", err)
		// TODO: Add search url to the reply?
		return nil, UserError{
			ChatID: m.Chat.Id,
			Err:    fmt.Errorf("Couldn't find definitions."),
		}
	}
	for _, d := range ds {
		if err := s.Telegram.SendMessage(&MessageReply{
			ChatId:    m.Chat.Id,
			Text:      d,
			ParseMode: "MarkdownV2",
			ReplyMarkup: &InlineKeyboardMarkup{
				InlineKeyboard: [][]*InlineKeyboard{[]*InlineKeyboard{
					learnIK(wordID),
				}},
			},
		}); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// Should never be called.
func (defaultCommand) OnCommand(*State, *Message) (Command, error) {
	return nil, nil
}

func joinCommands(m1, m2 map[string]CommandFactory) map[string]CommandFactory {
	r := make(map[string]CommandFactory)
	for k, v := range m1 {
		if _, dup := m2[k]; dup {
			panic(fmt.Sprintf("Found duplicated command: %q", k))
		}
		r[k] = v
	}
	for k, v := range m2 {
		// There can be no additional duplications so no need to check it.
		r[k] = v
	}
	return r
}

// textReply sends a message and resets state.
func textReply(text string) CommandFactory {
	return ReplyCommand(func(s *State, chatID int64) error {
		return s.Telegram.SendTextMessage(chatID, text)
	})
}

// SettingsCommands contains all settings-related commands. They are bundled
// together for convenience to have everything in one place.
var SettingsCommands = map[string]CommandFactory{
	"/language": SimpleQuestionCommandFactory(&SimpleSettingCommand{
		question: func() string {
			var ls []string
			for l, _ := range SupportedInputLanguages {
				ls = append(ls, fmt.Sprintf("%q", l))
			}
			sort.Strings(ls)
			return fmt.Sprintf("Enter input language of your choice. Supported are %s",
				strings.Join(ls, ","))
		}(),
		validate: func(s *State, answer string) error {
			return s.Settings.ValidateLanguage(answer)
		},
		save: func(s *State, chatID int64, answer string) error {
			return s.Settings.SetLanguage(chatID, answer)
		},
	}),
	"/timezone": SimpleQuestionCommandFactory(&SimpleSettingCommand{
		question: "Input your timezone in one of the formats: UTC, UTC+X or UTC-X.",
		validate: func(s *State, answer string) error {
			return s.Settings.ValidateTimeZone(answer)
		},
		save: func(s *State, chatID int64, answer string) error {
			return s.Settings.SetTimeZone(chatID, answer)
		},
	}),
}

var CommandsTemplate = struct {
	// When receiving a callback it will be matched here.
	// Tests should test that each callback is reachable.
	// Callbacks can rely only on info got from the CallbackQuery, and what is
	// set in CommandsTemplate.
	Callback map[CallbackAction]Callback
	// Each key in the map should directly correspond to the name of the command.
	// Possible improvement would be to allow regexps in the Commands name so
	// arguments can be passed there.
	Commands map[string]CommandFactory
	// Command returned by DefaultCommand shouldn't implement OnCommand. It
	// should not be ever called.
	DefaultCommand CommandFactory
}{
	Commands: joinCommands(
		map[string]CommandFactory{
			"/start": textReply(
				"Welcome to the language bot. Still in development. No instructions " +
					"so far. " +
					"All sentences and translations are from Tatoeba's (https://tatoeba.org) " +
					"dataset, released under a CC-BY 2.0 FR."),
			"/stop":     textReply("Stopped. Input the word to get it's definition."),
			"/practice": ReplyCommand(practiceReply),
			"/settings": ReplyCommand(settingsReply),
			"/stats":    ReplyCommand(statsReply),
			"/add":      AddCommandFactory(),
			"/delete":   DeleteCommandFactory(),
		},
		SettingsCommands,
	),
	Callback: map[CallbackAction]Callback{
		PracticeKnowAction:     KnowCallback{},
		PracticeDontKnowAction: DontKnowCallback{},
		ResetProgressAction:    DontKnowCallback{},
		SaveWordAction:         LearnCallback{},
		PracticeAnswerAction:   AnswerCallback{},
		ShowAnswerAction:       ShowAnswerCallback{},
	},
	DefaultCommand: func(string) Command { return defaultCommand{} },
}
