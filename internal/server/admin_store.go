package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode"

	"netsgo/pkg/fileutil"
	"netsgo/pkg/protocol"

	"golang.org/x/crypto/bcrypt"
)

// AdminData contains all persisted admin data
type AdminData struct {
	APIKeys      []APIKey           `json:"api_keys"`
	AdminUsers   []AdminUser        `json:"admin_users"`
	Clients      []RegisteredClient `json:"clients"`
	ClientTokens []ClientToken      `json:"client_tokens"` // client connection tokens
	ServerConfig ServerConfig       `json:"server_config"` // server configuration (set during initialization)
	Initialized  bool               `json:"initialized"`   // whether initialization has been completed
	JWTSecret    string             `json:"jwt_secret"`    // randomly generated JWT signing secret
	Sessions     []AdminSession     `json:"sessions"`      // list of server-side sessions
}

// AdminStore manages persistence of admin accounts, API Keys, and sessions
type AdminStore struct {
	path       string
	mu         sync.RWMutex
	data       AdminData
	bcryptCost int // 0 means use bcrypt.DefaultCost

	// timing-safe dummy hash matching bcryptCost; lazily initialized.
	dummyHashOnce sync.Once
	dummyHash     []byte

	// for testing only: inject a save failure on the next call to verify rollback paths.
	failSaveErr   error
	failSaveCount int
}

const tokenExpiryDuration = 7 * 24 * time.Hour // token inactivity expiry duration
const sessionDefaultTTL = 24 * time.Hour

var (
	errJWTSecretUnavailable = errors.New("jwt secret unavailable before initialization")
	errJWTSecretMissing     = errors.New("initialized admin store missing jwt secret")

	ErrClientTokenInvalid         = errors.New("client token invalid")
	ErrClientTokenRevoked         = errors.New("client token revoked")
	ErrClientTokenExpired         = errors.New("client token expired")
	ErrClientTokenInstallMismatch = errors.New("client token install mismatch")
)

func generateUUID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// NewAdminStore creates a new admin store
func NewAdminStore(path string) (*AdminStore, error) {
	store := &AdminStore{
		path:       path,
		bcryptCost: bcrypt.DefaultCost,
		data: AdminData{
			APIKeys:      []APIKey{},
			AdminUsers:   []AdminUser{},
			Clients:      []RegisteredClient{},
			ClientTokens: []ClientToken{},
			Sessions:     []AdminSession{},
		},
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create admin store directory: %w", err)
	}

	// attempt to load existing data
	if _, err := os.Stat(path); err == nil {
		if err := store.load(); err != nil {
			return nil, fmt.Errorf("failed to load admin config: %w", err)
		}
	}

	if err := store.validateLoadedState(); err != nil {
		return nil, err
	}

	// clean up expired sessions on startup
	if err := store.CleanExpiredSessions(); err != nil {
		return nil, fmt.Errorf("failed to clean expired sessions: %w", err)
	}

	if !store.data.Initialized {
		log.Printf("⚠️ Service not yet initialized; please use the install or init command to complete initialization")
	}

	return store, nil
}

func (s *AdminStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.data)
}

func (s *AdminStore) save() error {
	if s.failSaveErr != nil && s.failSaveCount > 0 {
		err := s.failSaveErr
		s.failSaveCount--
		if s.failSaveCount == 0 {
			s.failSaveErr = nil
		}
		return err
	}
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(s.path, data, 0600)
}

func cloneAdminData(data AdminData) AdminData {
	cloned := data
	cloned.APIKeys = append([]APIKey(nil), data.APIKeys...)
	cloned.AdminUsers = append([]AdminUser(nil), data.AdminUsers...)
	cloned.ClientTokens = append([]ClientToken(nil), data.ClientTokens...)
	cloned.Sessions = append([]AdminSession(nil), data.Sessions...)
	cloned.Clients = make([]RegisteredClient, len(data.Clients))
	copy(cloned.Clients, data.Clients)
	for i := range cloned.Clients {
		cloned.Clients[i].Stats = cloneSystemStats(data.Clients[i].Stats)
	}
	if len(data.ServerConfig.AllowedPorts) > 0 {
		cloned.ServerConfig.AllowedPorts = append([]PortRange(nil), data.ServerConfig.AllowedPorts...)
	}
	return cloned
}

func (s *AdminStore) saveWithRollbackLocked(previous AdminData) error {
	if err := s.save(); err != nil {
		s.data = previous
		return err
	}
	return nil
}

func (s *AdminStore) validateLoadedState() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data.Initialized && s.data.JWTSecret == "" {
		return fmt.Errorf("admin store invalid: %w", errJWTSecretMissing)
	}
	return nil
}

// ========== Initialization ==========

// IsInitialized checks whether the service has been initialized
func (s *AdminStore) IsInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Initialized
}

// Initialize performs one-time initialization
func (s *AdminStore) Initialize(username, password, serverAddr string, allowedPorts []PortRange) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data.Initialized {
		return fmt.Errorf("service is already initialized; cannot initialize again")
	}

	// validate password strength
	if err := validatePassword(password); err != nil {
		return fmt.Errorf("password does not meet requirements: %w", err)
	}

	// create admin account
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	s.data.AdminUsers = []AdminUser{{
		ID:           generateUUID(),
		Username:     username,
		PasswordHash: string(hash),
		Role:         "admin",
		CreatedAt:    time.Now(),
	}}

	// generate random JWT secret (32 bytes = 256 bits)
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return fmt.Errorf("failed to generate JWT secret: %w", err)
	}
	s.data.JWTSecret = hex.EncodeToString(secretBytes)

	// set server configuration
	s.data.ServerConfig = ServerConfig{
		ServerAddr:   serverAddr,
		AllowedPorts: allowedPorts,
	}

	s.data.Initialized = true

	log.Printf("✅ Service initialization complete, admin user: %s", username)
	return s.save()
}

// validatePassword validates password strength (at least 8 characters, must contain letters and digits)
func validatePassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	hasLetter := false
	hasDigit := false
	for _, c := range password {
		if unicode.IsLetter(c) {
			hasLetter = true
		}
		if unicode.IsDigit(c) {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("password must contain both letters and digits")
	}
	return nil
}

// ========== JWT Secret ==========

// GetJWTSecret returns the JWT signing secret
func (s *AdminStore) GetJWTSecret() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data.JWTSecret == "" {
		if !s.data.Initialized {
			return nil, errJWTSecretUnavailable
		}
		return nil, errJWTSecretMissing
	}
	return []byte(s.data.JWTSecret), nil
}

// ========== Server Config ==========

// GetServerConfig returns the current server configuration
func (s *AdminStore) GetServerConfig() ServerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.ServerConfig
}

// UpdateServerConfig updates the server configuration (can be modified after initialization)
func (s *AdminStore) UpdateServerConfig(config ServerConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.ServerConfig = config
	return s.save()
}

// ========== Port Whitelist ==========

// IsPortAllowed checks whether a port is within the allowed range.
// If the allowlist is empty (uninitialized), returns true (backward-compatible).
func (s *AdminStore) IsPortAllowed(port int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.data.ServerConfig.AllowedPorts) == 0 {
		return true // empty allowlist → no restriction
	}

	for _, pr := range s.data.ServerConfig.AllowedPorts {
		if port >= pr.Start && port <= pr.End {
			return true
		}
	}
	return false
}

// ========== AdminUsers ==========

// getDummyHash returns a timing-safe dummy hash matching the current bcryptCost;
// lazily initialized, computed only once per AdminStore instance.
func (s *AdminStore) getDummyHash() []byte {
	s.dummyHashOnce.Do(func() {
		h, _ := bcrypt.GenerateFromPassword([]byte("timing-safe-dummy"), s.bcryptCost)
		s.dummyHash = h
	})
	return s.dummyHash
}

func (s *AdminStore) ValidateAdminPassword(username, password string) (*AdminUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i, u := range s.data.AdminUsers {
		if u.Username == username {
			if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
				return nil, fmt.Errorf("incorrect username or password")
			}
			userCopy := s.data.AdminUsers[i]
			return &userCopy, nil
		}
	}
	// also run bcrypt compare when user is not found to maintain consistent timing and prevent timing side-channel attacks
	_ = bcrypt.CompareHashAndPassword(s.getDummyHash(), []byte(password))
	return nil, fmt.Errorf("incorrect username or password")
}

func (s *AdminStore) UpdateAdminLoginTime(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := cloneAdminData(s.data)
	for i, u := range s.data.AdminUsers {
		if u.ID == id {
			now := time.Now()
			s.data.AdminUsers[i].LastLogin = &now
			return s.saveWithRollbackLocked(previous)
		}
	}
	return nil
}

// ========== Clients ==========

func (s *AdminStore) GetOrCreateClient(installID string, info protocol.ClientInfo, remoteAddr string) (*RegisteredClient, error) {
	if installID == "" {
		return nil, fmt.Errorf("install_id must not be empty")
	}

	lastIP := remoteIP(remoteAddr)

	s.mu.Lock()
	defer s.mu.Unlock()

	previous := cloneAdminData(s.data)
	for i, client := range s.data.Clients {
		if client.InstallID == installID {
			s.data.Clients[i].Info = info
			s.data.Clients[i].LastSeen = time.Now()
			s.data.Clients[i].LastIP = lastIP
			if err := s.saveWithRollbackLocked(previous); err != nil {
				return nil, err
			}
			copy := s.data.Clients[i]
			return &copy, nil
		}
	}

	client := RegisteredClient{
		ID:        generateUUID(),
		InstallID: installID,
		Info:      info,
		CreatedAt: time.Now(),
		LastSeen:  time.Now(),
		LastIP:    lastIP,
	}
	s.data.Clients = append(s.data.Clients, client)
	if err := s.saveWithRollbackLocked(previous); err != nil {
		return nil, err
	}
	return &client, nil
}

func (s *AdminStore) TouchClient(clientID string, info protocol.ClientInfo, remoteAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := cloneAdminData(s.data)
	for i, client := range s.data.Clients {
		if client.ID == clientID {
			s.data.Clients[i].Info = info
			s.data.Clients[i].LastSeen = time.Now()
			if ip := remoteIP(remoteAddr); ip != "" {
				s.data.Clients[i].LastIP = ip
			}
			return s.saveWithRollbackLocked(previous)
		}
	}

	return fmt.Errorf("client %q not found", clientID)
}

func cloneSystemStats(stats *protocol.SystemStats) *protocol.SystemStats {
	if stats == nil {
		return nil
	}

	copy := *stats
	if len(stats.DiskPartitions) > 0 {
		copy.DiskPartitions = append([]protocol.DiskPartition(nil), stats.DiskPartitions...)
	}

	return &copy
}

func (s *AdminStore) UpdateClientStats(clientID string, info protocol.ClientInfo, stats protocol.SystemStats, remoteAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := cloneAdminData(s.data)
	for i, client := range s.data.Clients {
		if client.ID == clientID {
			s.data.Clients[i].Info = info
			s.data.Clients[i].LastSeen = time.Now()
			s.data.Clients[i].Stats = cloneSystemStats(&stats)
			if ip := remoteIP(remoteAddr); ip != "" {
				s.data.Clients[i].LastIP = ip
			}
			return s.saveWithRollbackLocked(previous)
		}
	}

	return fmt.Errorf("client %q not found", clientID)
}

func (s *AdminStore) GetRegisteredClients() []RegisteredClient {
	s.mu.RLock()
	defer s.mu.RUnlock()
	clients := make([]RegisteredClient, len(s.data.Clients))
	copy(clients, s.data.Clients)
	for i := range clients {
		clients[i].Stats = cloneSystemStats(clients[i].Stats)
	}
	return clients
}

func (s *AdminStore) GetRegisteredClient(clientID string) (RegisteredClient, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, client := range s.data.Clients {
		if client.ID == clientID {
			client.Stats = cloneSystemStats(client.Stats)
			return client, true
		}
	}
	return RegisteredClient{}, false
}

// ========== Display Name ==========

// UpdateClientDisplayName updates the custom display name for a Client
func (s *AdminStore) UpdateClientDisplayName(clientID, displayName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := cloneAdminData(s.data)
	for i, client := range s.data.Clients {
		if client.ID == clientID {
			s.data.Clients[i].DisplayName = displayName
			return s.saveWithRollbackLocked(previous)
		}
	}
	return fmt.Errorf("client %q not found", clientID)
}

// ========== Sessions ==========

// CreateSession creates a new session (removes existing sessions for the same user → single active session)
func (s *AdminStore) CreateSession(userID, username, role, ip, ua string) (*AdminSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := cloneAdminData(s.data)

	// remove all existing sessions for this user (enforce single active session)
	filtered := make([]AdminSession, 0, len(s.data.Sessions))
	for _, sess := range s.data.Sessions {
		if sess.UserID != userID {
			filtered = append(filtered, sess)
		}
	}

	session := AdminSession{
		ID:        generateUUID(),
		UserID:    userID,
		Username:  username,
		Role:      role,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(sessionDefaultTTL),
		IP:        ip,
		UserAgent: ua,
	}

	s.data.Sessions = append(filtered, session)
	for i, user := range s.data.AdminUsers {
		if user.ID == userID {
			now := time.Now()
			s.data.AdminUsers[i].LastLogin = &now
			break
		}
	}
	if err := s.saveWithRollbackLocked(previous); err != nil {
		return nil, err
	}
	return &session, nil
}

// GetSession retrieves the specified session (returns nil if not found or expired)
func (s *AdminStore) GetSession(sessionID string) *AdminSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i, sess := range s.data.Sessions {
		if sess.ID == sessionID {
			if time.Now().After(sess.ExpiresAt) {
				return nil // expired
			}
			copy := s.data.Sessions[i]
			return &copy
		}
	}
	return nil
}

// DeleteSession removes the specified session.
func (s *AdminStore) DeleteSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := cloneAdminData(s.data)
	filtered := make([]AdminSession, 0, len(s.data.Sessions))
	for _, sess := range s.data.Sessions {
		if sess.ID != sessionID {
			filtered = append(filtered, sess)
		}
	}
	s.data.Sessions = filtered
	return s.saveWithRollbackLocked(previous)
}

// DeleteSessionsByUserID removes all sessions belonging to the specified user.
func (s *AdminStore) DeleteSessionsByUserID(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := cloneAdminData(s.data)
	filtered := make([]AdminSession, 0, len(s.data.Sessions))
	for _, sess := range s.data.Sessions {
		if sess.UserID != userID {
			filtered = append(filtered, sess)
		}
	}
	s.data.Sessions = filtered
	return s.saveWithRollbackLocked(previous)
}

// CleanExpiredSessions removes all expired sessions.
func (s *AdminStore) CleanExpiredSessions() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	filtered := make([]AdminSession, 0, len(s.data.Sessions))
	cleaned := 0
	for _, sess := range s.data.Sessions {
		if now.Before(sess.ExpiresAt) {
			filtered = append(filtered, sess)
		} else {
			cleaned++
		}
	}
	if cleaned > 0 {
		previous := cloneAdminData(s.data)
		s.data.Sessions = filtered
		if err := s.saveWithRollbackLocked(previous); err != nil {
			return err
		}
		log.Printf("🧹 Cleaned up %d expired sessions", cleaned)
	}
	return nil
}

// ========== API Keys ==========

// ValidateClientKey checks whether the given key exists, is enabled, and has not expired.
// This only validates the key — it does not consume a use count (that happens in ExchangeToken).
func (s *AdminStore) ValidateClientKey(key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateClientKeyLocked(key)
}

// validateClientKeyLocked is an internal method; caller must already hold mu.
func (s *AdminStore) validateClientKeyLocked(key string) (bool, error) {
	if len(s.data.APIKeys) == 0 {
		if !s.data.Initialized {
			return false, fmt.Errorf("server not initialized; client connections are not accepted yet")
		}
		return false, fmt.Errorf("no API keys configured")
	}

	if key == "" {
		return false, fmt.Errorf("no valid API key provided and authentication is required")
	}

	for _, k := range s.data.APIKeys {
		if err := bcrypt.CompareHashAndPassword([]byte(k.KeyHash), []byte(key)); err == nil {
			if !k.IsActive {
				return false, fmt.Errorf("API key is disabled")
			}
			if k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()) {
				return false, fmt.Errorf("API key has expired")
			}
			if k.MaxUses > 0 && k.UseCount >= k.MaxUses {
				return false, fmt.Errorf("API key has reached its maximum use count")
			}
			return true, nil
		}
	}

	return false, fmt.Errorf("API key is invalid")
}

// findKeyIndexByRaw returns the index of the matching key, or -1 if not found. Caller must hold mu.
func (s *AdminStore) findKeyIndexByRaw(key string) int {
	for i, k := range s.data.APIKeys {
		if err := bcrypt.CompareHashAndPassword([]byte(k.KeyHash), []byte(key)); err == nil {
			return i
		}
	}
	return -1
}

// ========== Client Tokens ==========

// hashToken computes a SHA-256 hash of the given token.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// generateToken creates a random 256-bit token.
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}
	return "tk-" + hex.EncodeToString(buf), nil
}

// ExchangeToken exchanges a Key for a client token.
// If the given install_id already has a valid token, the existing token is refreshed
// and returned without consuming a key use count.
// Otherwise the key is validated, a use count is consumed, and a new token is issued.
func (s *AdminStore) ExchangeToken(key, installID, clientID, remoteAddr string) (string, *ClientToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ip := remoteIP(remoteAddr)

	// Check whether this install_id already has a valid (non-expired, non-revoked) token.
	for i, t := range s.data.ClientTokens {
		if t.InstallID == installID && !t.IsRevoked && time.Since(t.LastActiveAt) < tokenExpiryDuration {
			// Existing valid token found; refresh its hash instead of consuming the key.
			newToken, err := generateToken()
			if err != nil {
				return "", nil, err
			}
			previous := cloneAdminData(s.data)
			s.data.ClientTokens[i].TokenHash = hashToken(newToken)
			s.data.ClientTokens[i].LastActiveAt = time.Now()
			s.data.ClientTokens[i].LastIP = ip
			s.data.ClientTokens[i].ClientID = clientID
			if err := s.saveWithRollbackLocked(previous); err != nil {
				return "", nil, err
			}
			copy := s.data.ClientTokens[i]
			log.Printf("🔑 Token refreshed [install_id=%s]: reused existing valid token without consuming key", installID)
			return newToken, &copy, nil
		}
	}

	// No valid token; validate the key.
	valid, err := s.validateClientKeyLocked(key)
	if !valid {
		return "", nil, fmt.Errorf("key validation failed: %w", err)
	}

	// Consume a use count from the key.
	idx := s.findKeyIndexByRaw(key)
	previous := cloneAdminData(s.data)
	if idx >= 0 {
		s.data.APIKeys[idx].UseCount++
	}

	// Generate new token.
	newToken, err := generateToken()
	if err != nil {
		return "", nil, err
	}

	keyID := ""
	if idx >= 0 {
		keyID = s.data.APIKeys[idx].ID
	}

	clientToken := ClientToken{
		ID:           generateUUID(),
		TokenHash:    hashToken(newToken),
		InstallID:    installID,
		KeyID:        keyID,
		ClientID:     clientID,
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
		LastIP:       ip,
	}

	// Revoke all previous tokens for this install_id; only one active token per client is allowed.
	for i, t := range s.data.ClientTokens {
		if t.InstallID == installID && !t.IsRevoked {
			s.data.ClientTokens[i].IsRevoked = true
		}
	}

	s.data.ClientTokens = append(s.data.ClientTokens, clientToken)
	if err := s.saveWithRollbackLocked(previous); err != nil {
		return "", nil, err
	}

	log.Printf("🔑 Token exchanged [install_id=%s, key_id=%s]", installID, keyID)
	return newToken, &clientToken, nil
}

// ValidateClientToken verifies whether a client token is valid.
// Returns the matching ClientToken record if valid, otherwise returns an error.
func (s *AdminStore) ValidateClientToken(token, installID string) (*ClientToken, error) {
	if token == "" {
		return nil, ErrClientTokenInvalid
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tokenHash := hashToken(token)

	for i, t := range s.data.ClientTokens {
		if subtle.ConstantTimeCompare([]byte(t.TokenHash), []byte(tokenHash)) == 1 {
			// Token hash matched.
			if t.IsRevoked {
				return nil, ErrClientTokenRevoked
			}
			if t.InstallID != installID {
				log.Printf("⚠️ Token install_id mismatch: token_install=%s, req_install=%s", t.InstallID, installID)
				return nil, ErrClientTokenInstallMismatch
			}
			if time.Since(t.LastActiveAt) >= tokenExpiryDuration {
				return nil, ErrClientTokenExpired
			}
			// Validation passed; update last active time.
			previous := cloneAdminData(s.data)
			s.data.ClientTokens[i].LastActiveAt = time.Now()
			if err := s.saveWithRollbackLocked(previous); err != nil {
				return nil, err
			}
			copy := s.data.ClientTokens[i]
			return &copy, nil
		}
	}

	return nil, ErrClientTokenInvalid
}

// TouchToken updates the last-active time and IP address for the specified token.
func (s *AdminStore) TouchToken(tokenID, remoteAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := cloneAdminData(s.data)
	for i, t := range s.data.ClientTokens {
		if t.ID == tokenID {
			s.data.ClientTokens[i].LastActiveAt = time.Now()
			if ip := remoteIP(remoteAddr); ip != "" {
				s.data.ClientTokens[i].LastIP = ip
			}
			return s.saveWithRollbackLocked(previous)
		}
	}
	return nil
}

// RevokeToken revokes the specified token.
func (s *AdminStore) RevokeToken(tokenID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.data.ClientTokens {
		if t.ID == tokenID {
			s.data.ClientTokens[i].IsRevoked = true
			return s.save()
		}
	}
	return fmt.Errorf("token %q not found", tokenID)
}

// RevokeTokensByKeyID revokes all tokens associated with the given key ID, returning the count revoked.
func (s *AdminStore) RevokeTokensByKeyID(keyID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	previous := cloneAdminData(s.data)
	for i, t := range s.data.ClientTokens {
		if t.KeyID == keyID && !t.IsRevoked {
			s.data.ClientTokens[i].IsRevoked = true
			count++
		}
	}
	if count > 0 {
		if err := s.saveWithRollbackLocked(previous); err != nil {
			return 0, err
		}
	}
	return count, nil
}

// CleanExpiredTokens removes tokens that have been inactive for more than 7 days and revoked tokens.
func (s *AdminStore) CleanExpiredTokens() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	filtered := make([]ClientToken, 0, len(s.data.ClientTokens))
	cleaned := 0
	for _, t := range s.data.ClientTokens {
		if t.IsRevoked || now.Sub(t.LastActiveAt) >= tokenExpiryDuration {
			cleaned++
			continue
		}
		filtered = append(filtered, t)
	}
	if cleaned > 0 {
		previous := cloneAdminData(s.data)
		s.data.ClientTokens = filtered
		if err := s.saveWithRollbackLocked(previous); err != nil {
			return err
		}
		log.Printf("🧹 Cleaned up %d expired/revoked tokens", cleaned)
	}
	return nil
}

// GetTokensByKeyID returns all tokens exchanged by a given key.
func (s *AdminStore) GetTokensByKeyID(keyID string) []ClientToken {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]ClientToken, 0)
	for _, t := range s.data.ClientTokens {
		if t.KeyID == keyID {
			result = append(result, t)
		}
	}
	return result
}

// GetClientTokenByInstallID returns the active token for a given install_id, if any.
func (s *AdminStore) GetClientTokenByInstallID(installID string) *ClientToken {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, t := range s.data.ClientTokens {
		if t.InstallID == installID && !t.IsRevoked && time.Since(t.LastActiveAt) < tokenExpiryDuration {
			copy := t
			return &copy
		}
	}
	return nil
}

func (s *AdminStore) AddAPIKey(name, keyString string, permissions []string, expiresAt *time.Time) (*APIKey, error) {
	permissions, err := normalizeKeyPermissions(permissions)
	if err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(keyString), s.bcryptCost)
	if err != nil {
		return nil, err
	}

	k := APIKey{
		ID:          generateUUID(),
		Name:        name,
		KeyHash:     string(hash),
		Permissions: permissions,
		CreatedAt:   time.Now(),
		ExpiresAt:   expiresAt,
		IsActive:    true,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	previous := cloneAdminData(s.data)
	s.data.APIKeys = append(s.data.APIKeys, k)
	if err := s.saveWithRollbackLocked(previous); err != nil {
		return nil, err
	}

	return &k, nil
}

func (s *AdminStore) GetAPIKeys() []APIKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]APIKey, len(s.data.APIKeys))
	copy(keys, s.data.APIKeys)
	return keys
}

func (s *AdminStore) SetAPIKeyActive(id string, active bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, key := range s.data.APIKeys {
		if key.ID == id {
			s.data.APIKeys[i].IsActive = active
			return s.save()
		}
	}
	return fmt.Errorf("API key %q not found", id)
}

func (s *AdminStore) DeleteAPIKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := make([]APIKey, 0, len(s.data.APIKeys))
	found := false
	for _, key := range s.data.APIKeys {
		if key.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, key)
	}
	if !found {
		return fmt.Errorf("API key %q not found", id)
	}

	s.data.APIKeys = filtered
	return s.save()
}

// SetAPIKeyMaxUses sets the maximum use count for an API key.
func (s *AdminStore) SetAPIKeyMaxUses(id string, maxUses int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, key := range s.data.APIKeys {
		if key.ID == id {
			s.data.APIKeys[i].MaxUses = maxUses
			return s.save()
		}
	}
	return fmt.Errorf("API key %q not found", id)
}

func normalizeKeyPermissions(permissions []string) ([]string, error) {
	if len(permissions) == 0 {
		return []string{"connect"}, nil
	}

	normalized := make([]string, 0, len(permissions))
	seen := map[string]struct{}{}
	for _, permission := range permissions {
		if permission != "connect" {
			return nil, fmt.Errorf("unsupported API key permission: %s", permission)
		}
		if _, ok := seen[permission]; ok {
			continue
		}
		seen[permission] = struct{}{}
		normalized = append(normalized, permission)
	}
	return normalized, nil
}

func remoteIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	// 统一 IPv6 loopback 为 IPv4
	if host == "::1" {
		return "127.0.0.1"
	}
	return host
}
