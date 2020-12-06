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
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRepetition(t *testing.T) {
	dir, err := ioutil.TempDir("", "repetition")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Temp dir: %q", dir)
	defer os.RemoveAll(dir)

	db := filepath.Join(dir, "tmpdb")
	stages := []time.Duration{0, 0, 0, 0}
	r, err := NewRepetition(db, stages)
	if err != nil {
		t.Fatal(err)
	}
	// Check that running init twice doesn't cause any issues.
	r, err = NewRepetition(db, stages)
	if err != nil {
		t.Fatal(err)
	}

	type row struct {
		chatId     int64
		word       string
		definition string
		ease       int64
		ivl        int64
	}

	count := func(q *row) int32 {
		t.Helper()
		row := r.db.QueryRow(`
			SELECT COUNT(*) FROM Repetition
			WHERE chat_id = $1
				AND word = $2
				AND definition = $3
				AND ease = $4
				AND ivl = $5`,
			q.chatId, q.word, q.definition, q.ease, q.ivl)
		var d int32
		if err := row.Scan(&d); err != nil {
			t.Errorf("Scanning Row %v: %v", q, err)
		}
		return d
	}
	check := func(q *row) {
		t.Helper()
		if count(q) <= 0 {
			t.Errorf("Scanning Row %#v: No rows found", q)
			t.Log("Dump of the `Repetition` table:")
			rows, err := r.db.Query(`SELECT * FROM Repetition;`)
			if err != nil {
				t.Error(err)
				return
			}
			defer rows.Close()
			cols, err := rows.Columns()
			if err != nil {
				t.Error(err)
				return
			}
			for rows.Next() {
				record := make([][]byte, len(cols))
				args := make([]interface{}, len(cols))
				for i, _ := range cols {
					record[i] = []byte{}
					args[i] = &record[i]
				}
				if err := rows.Scan(args...); err != nil {
					t.Error(err)
					return
				}
				sr := make([]string, len(cols))
				for i, r := range record {
					sr[i] = cols[i] + ": " + string(r) + "||"
				}
				t.Logf("%v", sr)
			}
		}
	}

	const chatId int64 = 1
	if err := r.Save(chatId, "foo", "foo is bar", ""); err != nil {
		t.Fatal(err)
	}
	check(&row{chatId: chatId, word: "foo", definition: "foo is bar", ease: 250, ivl: 0})

	d, err := r.Repeat(chatId)
	if err != nil {
		t.Fatal(err)
	}
	if d != "******** is bar" {
		t.Errorf("got %q; want %q", d, "foo is bar")
	}
	check(&row{chatId: chatId, word: "foo", definition: "foo is bar", ease: 250, ivl: 0})

	if err := r.Answer(chatId, "foo", AnswerAgain); err != nil {
		t.Fatal(err)
	}
	check(&row{chatId: chatId, word: "foo", definition: "foo is bar", ease: 230, ivl: 0})

	for _, tc := range []struct {
		ease     AnswerEase
		wantEase int64
		wantIvl  int64
	}{
		{AnswerGood, 230, 1},
		{AnswerGood, 230, 3},
		{AnswerGood, 230, 6},
		{AnswerGood, 230, 13},
		{AnswerAgain, 210, 0},
		{AnswerEasy, 225, 1},
		{AnswerEasy, 240, 3},
		{AnswerEasy, 255, 9},
		{AnswerHard, 240, 10},
	} {
		if err := r.Answer(chatId, "foo", tc.ease); err != nil {
			t.Fatal(err)
		}
		check(&row{chatId: chatId, word: "foo", definition: "foo is bar", ease: tc.wantEase, ivl: tc.wantIvl})
	}

	if e, err := r.Exists(chatId, "foo"); err != nil || !e {
		t.Errorf("r.Exists: %t, %v want true, nil", e, err)
	}
	if err := r.Delete(chatId, "foo"); err != nil {
		t.Fatal(err)
	}
	if count(&row{chatId: chatId, word: "foo", definition: "foo is bar", ivl: 3}) > 0 {
		t.Errorf("%q wasn't deleted", "foo")
	}
	// consecutive deletions of the row result in no error
	if e, err := r.Exists(chatId, "foo"); err != nil || e {
		t.Errorf("r.Exists: %t, %v want false, nil", e, err)
	}
	if err := r.Delete(chatId, "foo"); err != nil {
		t.Fatal(err)
	}
}
