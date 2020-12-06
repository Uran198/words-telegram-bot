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
	"math"
	"strings"
	"time"
)

type Repetition struct {
	db          *sql.DB
	initialEase int
	initialIvl  int64
	againDelay  time.Duration
}

func NewRepetition(dbPath string, stages []time.Duration) (*Repetition, error) {
	// this is arbitrary big number
	const maxStages = 1_000_000
	if len(stages) == 0 {
		panic("stages == 0")
	}
	if len(stages) >= maxStages {
		panic(fmt.Sprintf("too many stages; should be less than %d", maxStages))
	}
	var sv []string
	for k, s := range stages {
		sv = append(sv, fmt.Sprintf("(%d, %d)", k, int64(s.Seconds())))
	}
	// insert large last id so that words with stages > len(stages) can still
	// be queried (This can happen if number of stages shrinks)
	sv = append(sv, fmt.Sprintf("(%d, %d)", maxStages, int64(stages[len(stages)-1].Seconds())))
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS Repetition (
			chat_id INTEGER,
			word STRING,
			definition STRING,
			stage INTEGER, -- obsolete
			last_updated_seconds INTEGER -- seconds since UNIX epoch
		);
		CREATE TEMP TABLE IF NOT EXISTS Stages (
			id INTEGER,
			duration INTEGER
		);
		INSERT INTO Stages(id, duration)
			VALUES ` +
		// Usually not escaping sql parts can lead to sql injection. In
		// this case it's more convenient, and only numbers are put inside.
		strings.Join(sv, ","),
	); err != nil {
		return nil, err
	}
	if _, err := db.Exec(strings.Join([]string{
		// next_review_seconds -- seconds since UNIX epoch for the next review.
		`ALTER TABLE Repetition ADD COLUMN next_review_seconds INTEGER`,
		// current ease and interval for the card.
		`ALTER TABLE Repetition ADD COLUMN ease INTEGER`,
		`ALTER TABLE Repetition ADD COLUMN ivl INTEGER`,
	}, ";")); err != nil {
		// There is no way to add column if it doesn't exists only, so we have
		// to ignore an error here. Matching on the error text is not a good
		// style, however there is no type that can be matched.
		if !strings.Contains(err.Error(), "duplicate column name") {
			return nil, err
		}
	}
	if _, err := db.Exec(strings.Join([]string{
		// markup of the definition json encoded.
		`ALTER TABLE Repetition ADD COLUMN definition_entities STRING`,
	}, ";")); err != nil {
		// There is no way to add column if it doesn't exists only, so we have
		// to ignore an error here. Matching on the error text is not a good
		// style, however there is no type that can be matched.
		if !strings.Contains(err.Error(), "duplicate column name") {
			return nil, err
		}
	}
	// Set next_review_seconds, otherwise all cards not using next_review_seconds are lost!
	const (
		initialEase = 250
		initialIvl  = 0
	)
	if _, err := db.Exec(
		`UPDATE Repetition
		SET
			next_review_seconds = last_updated_seconds + (
				SELECT Stages.duration
				FROM Stages
				WHERE Stages.id = Repetition.stage),
			ease = $1,
			ivl = $2
		WHERE
			next_review_seconds IS NULL;`,
		initialEase,
		initialIvl,
	); err != nil {
		return nil, err
	}
	row := db.QueryRow(`
		SELECT COUNT(*)
		FROM Repetition;`)
	var d int
	if err := row.Scan(&d); err != nil {
		return nil, err
	}
	log.Printf("DEBUG: Repetition database initially contains %d rows!", d)
	return &Repetition{
		db: db,
		// TODO: Eventually these should be configurable by the user.
		initialEase: initialEase,
		initialIvl:  initialIvl,
		againDelay:  20 * time.Second,
	}, nil
}

type RepetitionStats struct {
	// Number of words user has saved for learning.
	WordCount int
}

func (r *Repetition) Stats(chatID int64) (*RepetitionStats, error) {
	row := r.db.QueryRow(`
			SELECT COUNT(*) FROM Repetition
			WHERE chat_id = $1`,
		chatID)
	stats := new(RepetitionStats)
	if err := row.Scan(&stats.WordCount); err != nil {
		return nil, fmt.Errorf("counting rows for chat %d: %w", chatID, err)
	}
	return stats, nil
}

func (r *Repetition) Save(chatID int64, word, definition, entities string) error {
	// FIXME: Don't insert duplicates!
	t := time.Now().Unix()
	_, err := r.db.Exec(`
		INSERT INTO Repetition(chat_id,
			word, definition, definition_entities, stage,
			ease, ivl,
			last_updated_seconds, next_review_seconds)
		VALUES($0, $1, $2, $3, $4, $5, $6, $7, $8)`,
		chatID, word, definition, entities, 0,
		r.initialEase, r.initialIvl,
		t, t+r.initialIvl*int64(time.Hour.Seconds()))
	return err
}

// Repeat retrieves a definitions of the word ready for repetition.
func (r *Repetition) Repeat(chatID int64) (string, error) {
	// TODO: Can consider ordering by oldest
	// TODO: Add a test for this somehow to make sure that correct amount of
	// time is waited. (can modify last_updated_seconds inside the test to
	// simulate time)
	row := r.db.QueryRow(`
		SELECT word, definition
		FROM Repetition
		WHERE Repetition.next_review_seconds <= $0
		  AND Repetition.chat_id = $1;`,
		time.Now().Unix(), chatID)
	var w, d string
	err := row.Scan(&w, &d)
	if err != nil {
		return "", err
	}
	// strip first paragraph which corresponds to the word in question.
	if s := strings.Split(d, "\n\n"); len(s) > 1 {
		d = strings.Join(s[1:], "\n\n")
	}
	// Make sure that the word is not in the question.
	return strings.ReplaceAll(d, w, "********"), nil
}

// Repeat retrieves a word ready for repetition.
// TODO: Deduplicate with Repeat?
func (r *Repetition) RepeatWord(chatID int64) (string, error) {
	row := r.db.QueryRow(`
		SELECT word
		FROM Repetition
		WHERE Repetition.next_review_seconds <= $0
		  AND Repetition.chat_id = $1;`,
		time.Now().Unix(), chatID)
	var w string
	err := row.Scan(&w)
	return w, err
}

type AnswerEase int

const (
	AnswerAgain AnswerEase = iota
	AnswerHard
	AnswerGood
	AnswerEasy
)

type Schedule struct {
	ivl                  int64
	ease                 int64
	last_updated_seconds int64
	next_review_seconds  int64
}

func (r *Repetition) CalcSchedule(chatID int64, word string, answ AnswerEase) (*Schedule, error) {
	// Following scheduling algorithm is based on the one used by Anki, but
	// without differentiation between word that is being learned, relearned,
	// or studied. It might be worth adding that as well in the future.
	// TODO: Make configurable.
	const easyBonus = 1.3

	row := r.db.QueryRow(`
		SELECT ease, ivl, last_updated_seconds
		FROM Repetition
		WHERE Repetition.word = $0
		  AND Repetition.chat_id = $1;`,
		word, chatID)
	var ease, ivl, last_update int64
	if err := row.Scan(&ease, &ivl, &last_update); err != nil {
		return nil, err
	}
	// Correct ivl for the actual time since previous review.
	if d := int64(time.Now().Sub(time.Unix(last_update, 0)).Hours() / 24); d > ivl {
		ivl = d
	}

	mult := 1.0
	switch answ {
	case AnswerAgain:
		ease -= 20
	case AnswerHard:
		ease -= 15
		mult = 1.2
	case AnswerGood:
		mult = float64(ease) / 100.0
	case AnswerEasy:
		ease += 15
		mult = float64(ease) * easyBonus / 100.0
	}
	mult = math.Min(mult, 13)
	if ease < 130 {
		ease = 130
	} else if ease > 1300 {
		ease = 1300
	}
	t := time.Now().Unix()
	var nr int64
	if answ == AnswerAgain {
		ivl = 0
		nr = t + int64(r.againDelay.Seconds())
	} else {
		switch ivl {
		// The previous answer was Again, so we reset interval to 1 day.
		case 0:
			ivl = 1
		case 1:
			ivl = 3
		default:
			nivl := int64(float64(ivl) * mult)
			// Always increase interval at least by 1.
			if nivl == ivl {
				ivl += 1
			} else {
				ivl = nivl
			}
		}
		nr = t + ivl*int64(time.Hour.Seconds()*24)
	}
	return &Schedule{
		ivl:                  ivl,
		ease:                 ease,
		last_updated_seconds: t,
		next_review_seconds:  nr,
	}, nil
}

func (r *Repetition) Answer(chatID int64, word string, answ AnswerEase) error {
	sc, err := r.CalcSchedule(chatID, word, answ)
	if err != nil {
		return err
	}
	if _, err = r.db.Exec(`
		UPDATE Repetition
		SET
			ease = $0,
			ivl = $1,
			last_updated_seconds = $2,
			next_review_seconds = $3
		WHERE word = $5
		  AND chat_id = $6;`,
		sc.ease, sc.ivl, sc.last_updated_seconds, sc.next_review_seconds,
		word, chatID,
	); err != nil {
		return fmt.Errorf("INTERNAL: Failed updating learning intervals: %w", err)
	}
	return nil
}

// FIXME: Too many return valuables. Create a struct to hold it. The same for Save.
func (r *Repetition) GetDefinition(chatID int64, word string) (def string, entities string, err error) {
	row := r.db.QueryRow(`
		SELECT definition, definition_entities
		FROM Repetition
		WHERE word = $0
		  AND chat_id = $1`,
		word, chatID)
	var e sql.NullString
	if err = row.Scan(&def, &e); err != nil {
		err = fmt.Errorf("INTERNAL: Did not find definition: %w", err)
		return
	}
	if e.Valid {
		entities = e.String
	}
	return
}

func (r *Repetition) Exists(chatID int64, word string) (bool, error) {
	row := r.db.QueryRow(`
			SELECT COUNT(*) FROM Repetition
			WHERE chat_id = $1
				AND word = $2`,
		chatID, word)
	var d int32
	if err := row.Scan(&d); err != nil {
		return false, fmt.Errorf("INTERNAL: Counting %q for chat %d: %w", word, chatID, err)
	}
	return d > 0, nil
}

func (r *Repetition) Delete(chatID int64, word string) error {
	_, err := r.db.Exec(`
		DELETE
		FROM Repetition
		WHERE word = $0
		  AND chat_id = $1`,
		word, chatID)
	if err != nil {
		return fmt.Errorf("Failed deleting %q: %w", word, err)
	}
	return nil
}

// TODO later editing should be helpful.
// func (r *Repetition) Edit(chatID int64, word, newDefinition string) {
// }
