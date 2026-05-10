package jjctl

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type SecretStore struct {
	keyPath   string
	storePath string
	key       []byte
	mu        sync.Mutex
}

type secretFile struct {
	Version int                       `json:"version"`
	Items   map[string]secretFileItem `json:"items"`
}

type secretFileItem struct {
	Label      string `json:"label"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	CreatedAt  string `json:"created_at"`
}

func OpenSecretStore(keyPath, storePath string) (*SecretStore, error) {
	key, err := loadOrCreateSecretKey(keyPath)
	if err != nil {
		return nil, err
	}
	return &SecretStore{keyPath: keyPath, storePath: storePath, key: key}, nil
}

func loadOrCreateSecretKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		key, err := base64.StdEncoding.DecodeString(string(data))
		if err != nil {
			return nil, err
		}
		if len(key) != 32 {
			return nil, errors.New("invalid local secret key length")
		}
		return key, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(key))
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *SecretStore) Save(label string, plaintext []byte, createdAt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := s.readFile()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ref := "local-aesgcm:" + newID("sec")
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(ref))
	file.Items[ref] = secretFileItem{
		Label:      label,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
		CreatedAt:  createdAt,
	}
	if err := s.writeFile(file); err != nil {
		return "", err
	}
	return ref, nil
}

func (s *SecretStore) Load(ref string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := s.readFile()
	if err != nil {
		return nil, err
	}
	item, ok := file.Items[ref]
	if !ok {
		return nil, errors.New("secret ref not found")
	}
	nonce, err := base64.StdEncoding.DecodeString(item.Nonce)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(item.Ciphertext)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, []byte(ref))
}

func (s *SecretStore) Delete(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := s.readFile()
	if err != nil {
		return err
	}
	delete(file.Items, ref)
	return s.writeFile(file)
}

func (s *SecretStore) readFile() (secretFile, error) {
	file := secretFile{Version: 1, Items: map[string]secretFileItem{}}
	data, err := os.ReadFile(s.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return file, nil
		}
		return file, err
	}
	if len(data) == 0 {
		return file, nil
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return file, err
	}
	if file.Items == nil {
		file.Items = map[string]secretFileItem{}
	}
	if file.Version == 0 {
		file.Version = 1
	}
	return file, nil
}

func (s *SecretStore) writeFile(file secretFile) error {
	if err := os.MkdirAll(filepath.Dir(s.storePath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.storePath, data, 0o600)
}
