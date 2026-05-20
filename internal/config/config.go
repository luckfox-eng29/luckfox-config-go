/*
 * SPDX-FileCopyrightText: 2026 Luckfox Team
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// Package config manages /etc/luckfox-config.json, a simple key=value persistent store in JSON.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"syscall"
)

const DefaultPath = "/etc/luckfox-config.json"

// Store is a thread-safe key=value config file with modular structure.
type Store struct {
	path string
	mu   sync.RWMutex
	data map[string]map[string]string
}

// Open reads (or creates) the config file and returns a Store.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: make(map[string]map[string]string)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	f, err := os.OpenFile(s.path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("open config %s: %w", s.path, err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if stat.Size() == 0 {
		return nil
	}

	decoder := json.NewDecoder(f)
	if err := decoder.Decode(&s.data); err != nil {
		return fmt.Errorf("decode config %s: %w", s.path, err)
	}
	return nil
}

// Get returns the value for key in the given module, or "" if not set.
func (s *Store) Get(module, key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m, ok := s.data[module]; ok {
		return m[key]
	}
	return ""
}

// GetDefault returns the value for key in module, or defaultVal if not set.
func (s *Store) GetDefault(module, key, defaultVal string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m, ok := s.data[module]; ok {
		if v, ok := m[key]; ok && v != "" {
			return v
		}
	}
	return defaultVal
}

// Set writes or updates a key in a module, persisting to disk.
func (s *Store) Set(module, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data[module] == nil {
		s.data[module] = make(map[string]string)
	}
	s.data[module][key] = value
	return s.flush()
}

// SetMulti sets multiple keys for a module atomically (single flush).
func (s *Store) SetMulti(module string, pairs map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data[module] == nil {
		s.data[module] = make(map[string]string)
	}
	for k, v := range pairs {
		s.data[module][k] = v
	}
	return s.flush()
}

// Delete removes a key from a module in the store.
func (s *Store) Delete(module, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.data[module]; ok {
		delete(m, key)
		return s.flush()
	}
	return nil
}

// flush rewrites the file. Caller must hold mu.
func (s *Store) flush() error {
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("write config tmp: %w", err)
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s.data); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	syscall.Sync()
	return nil
}
