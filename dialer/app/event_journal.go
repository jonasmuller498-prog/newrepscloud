package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var errMalformedJournal = errors.New(
	"malformed ARI journal record requires operator action")

type EventJournal struct {
	dir string
}

type journalEntry struct {
	Record ARIJournalRecord
	path   string
	modNS  int64
}

func NewEventJournal(dir string) *EventJournal {
	return &EventJournal{dir: dir}
}

func (j *EventJournal) Probe() error {
	file, err := os.CreateTemp(j.dir, ".health-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write([]byte("ok"))
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err = os.Remove(name); err != nil {
		return err
	}
	return syncDirectory(j.dir)
}

func (j *EventJournal) Put(record ARIJournalRecord) (journalEntry, error) {
	if err := record.Validate(); err != nil {
		return journalEntry{}, err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return journalEntry{}, err
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(j.dir, ".pending-")
	if err != nil {
		return journalEntry{}, err
	}
	temp := file.Name()
	installed := false
	defer func() {
		if !installed {
			_ = os.Remove(temp)
		}
	}()
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return journalEntry{}, err
	}
	final := filepath.Join(j.dir, record.Key+".json")
	if err = os.Rename(temp, final); err != nil {
		return journalEntry{}, err
	}
	installed = true
	entry := journalEntry{Record: record, path: final}
	if err = syncDirectory(j.dir); err != nil {
		return entry, err
	}
	return entry, nil
}

func (j *EventJournal) Entries() ([]journalEntry, error) {
	items, err := os.ReadDir(j.dir)
	if err != nil {
		return nil, err
	}
	entries := make([]journalEntry, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(item.Name(), ".health-") {
			continue
		}
		entry, readErr := j.readEntry(item)
		if readErr != nil {
			return nil, errMalformedJournal
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].modNS == entries[right].modNS {
			return entries[left].path < entries[right].path
		}
		return entries[left].modNS < entries[right].modNS
	})
	return entries, nil
}

func (j *EventJournal) readEntry(item os.DirEntry) (journalEntry, error) {
	if item.IsDir() || item.Type()&os.ModeSymlink != 0 {
		return journalEntry{}, errMalformedJournal
	}
	info, err := item.Info()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 2 || info.Size() > 4096 {
		return journalEntry{}, errMalformedJournal
	}
	path := filepath.Join(j.dir, item.Name())
	data, err := os.ReadFile(path)
	if err != nil {
		return journalEntry{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record ARIJournalRecord
	if err = decoder.Decode(&record); err != nil || record.Validate() != nil {
		return journalEntry{}, errMalformedJournal
	}
	if err = ensureJSONEnd(decoder); err != nil {
		return journalEntry{}, errMalformedJournal
	}
	finalName := record.Key + ".json"
	if item.Name() != finalName && !strings.HasPrefix(item.Name(), ".pending-") {
		return journalEntry{}, errMalformedJournal
	}
	return journalEntry{Record: record, path: path, modNS: info.ModTime().UnixNano()}, nil
}

func (j *EventJournal) Delete(entry journalEntry) error {
	err := os.Remove(entry.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(j.dir)
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return errMalformedJournal
}

func syncDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
