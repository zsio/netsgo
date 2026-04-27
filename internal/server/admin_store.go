package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
	"unicode"

	"netsgo/pkg/protocol"

	"golang.org/x/crypto/bcrypt"
)

// AdminStore manages persistence of admin accounts, API Keys, and sessions.
type AdminStore struct {
	path      string
	db        *sql.DB
	closeDB   bool
	mu        sync.RWMutex
	closeOnce sync.Once
	closeErr  error

	bcryptCost int // 0 means use bcrypt.DefaultCost

	// timing-safe dummy hash matching bcryptCost; lazily initialized.
	dummyHashOnce sync.Once
	dummyHash     []byte

	// for testing only: inject a save failure before the next transaction commit.
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
	ErrRegisteredClientNotFound   = errors.New("registered client not found")
)

type dbScanner interface {
	Scan(dest ...any) error
}

type dbQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

type dbExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func generateUUID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// NewAdminStore creates a new admin store.
func NewAdminStore(path string) (*AdminStore, error) {
	db, err := openServerDB(path)
	if err != nil {
		return nil, err
	}

	store, err := newAdminStoreWithDB(path, db, true)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func newAdminStoreWithDB(path string, db *sql.DB, closeDB bool) (*AdminStore, error) {
	store := &AdminStore{
		path:       path,
		db:         db,
		closeDB:    closeDB,
		bcryptCost: bcrypt.DefaultCost,
	}

	if err := store.validateLoadedState(); err != nil {
		return nil, err
	}

	// clean up expired sessions on startup
	if err := store.CleanExpiredSessions(); err != nil {
		return nil, fmt.Errorf("failed to clean expired sessions: %w", err)
	}

	if !store.IsInitialized() {
		log.Printf("⚠️ Service not yet initialized; please use the install or init command to complete initialization")
	}

	return store, nil
}

func (s *AdminStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if !s.closeDB {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closeErr = s.db.Close()
	})
	return s.closeErr
}

func (s *AdminStore) maybeFailSave() error {
	if s.failSaveErr != nil && s.failSaveCount > 0 {
		err := s.failSaveErr
		s.failSaveCount--
		if s.failSaveCount == 0 {
			s.failSaveErr = nil
		}
		return err
	}
	return nil
}

func rollbackUnlessCommitted(tx *sql.Tx, committed *bool) {
	if !*committed {
		_ = tx.Rollback()
	}
}

func commitTx(tx *sql.Tx, committed *bool) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	*committed = true
	return nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, raw)
}

func nullableTimeValue(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTime(t)
}

func parseOptionalTime(raw sql.NullString) (*time.Time, error) {
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	parsed, err := parseTime(raw.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseOptionalTimeValue(raw sql.NullString) (time.Time, error) {
	ptr, err := parseOptionalTime(raw)
	if err != nil || ptr == nil {
		return time.Time{}, err
	}
	return *ptr, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func intToBool(value int) bool {
	return value != 0
}

const maxSQLiteInt64 = uint64(1<<63 - 1)

func sqliteUint64(field string, value int64) (uint64, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s contains negative persisted value %d", field, value)
	}
	return uint64(value), nil
}

func sqliteInt64(field string, value uint64) (int64, error) {
	if value > maxSQLiteInt64 {
		return 0, fmt.Errorf("%s value %d exceeds SQLite INTEGER max %d", field, value, maxSQLiteInt64)
	}
	return int64(value), nil
}

func (s *AdminStore) validateLoadedState() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	initialized, jwtSecret, err := s.loadConfigLifecycle(s.db)
	if err != nil {
		return err
	}
	if initialized && jwtSecret == "" {
		return fmt.Errorf("admin store invalid: %w", errJWTSecretMissing)
	}
	return nil
}

func (s *AdminStore) loadConfigLifecycle(q dbQuerier) (bool, string, error) {
	var initialized int
	var jwtSecret string
	err := q.QueryRow(`SELECT initialized, jwt_secret FROM server_config WHERE id = 1`).Scan(&initialized, &jwtSecret)
	if err == sql.ErrNoRows {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("load server config lifecycle: %w", err)
	}
	return intToBool(initialized), jwtSecret, nil
}

// ========== Initialization ==========

// IsInitialized checks whether the service has been initialized.
func (s *AdminStore) IsInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	initialized, _, err := s.loadConfigLifecycle(s.db)
	return err == nil && initialized
}

// Initialize performs one-time initialization.
func (s *AdminStore) Initialize(username, password, serverAddr string, allowedPorts []PortRange) error {
	if err := validatePassword(password); err != nil {
		return fmt.Errorf("password does not meet requirements: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return fmt.Errorf("failed to generate JWT secret: %w", err)
	}
	jwtSecret := hex.EncodeToString(secretBytes)

	now := time.Now()
	user := AdminUser{
		ID:           generateUUID(),
		Username:     username,
		PasswordHash: string(hash),
		Role:         "admin",
		CreatedAt:    now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	initialized, _, err := s.loadConfigLifecycle(tx)
	if err != nil {
		return err
	}
	if initialized {
		return fmt.Errorf("service is already initialized; cannot initialize again")
	}

	if _, err := tx.Exec(`DELETE FROM admin_users`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO admin_users (id, username, password_hash, role, created_at, last_login) VALUES (?, ?, ?, ?, ?, NULL)`,
		user.ID, user.Username, user.PasswordHash, user.Role, formatTime(user.CreatedAt)); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO server_config (id, initialized, jwt_secret, server_addr)
		VALUES (1, 1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET initialized = 1, jwt_secret = excluded.jwt_secret, server_addr = excluded.server_addr`,
		jwtSecret, serverAddr); err != nil {
		return err
	}
	if err := replaceAllowedPorts(tx, allowedPorts); err != nil {
		return err
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	if err := commitTx(tx, &committed); err != nil {
		return err
	}

	log.Printf("✅ Service initialization complete, admin user: %s", username)
	return nil
}

// validatePassword validates password strength (at least 8 characters, must contain letters and digits).
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

// GetJWTSecret returns the JWT signing secret.
func (s *AdminStore) GetJWTSecret() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	initialized, jwtSecret, err := s.loadConfigLifecycle(s.db)
	if err != nil {
		return nil, err
	}
	if jwtSecret == "" {
		if !initialized {
			return nil, errJWTSecretUnavailable
		}
		return nil, errJWTSecretMissing
	}
	return []byte(jwtSecret), nil
}

// ========== Server Config ==========

// GetServerConfig returns the current server configuration.
func (s *AdminStore) GetServerConfig() ServerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var config ServerConfig
	err := s.db.QueryRow(`SELECT server_addr FROM server_config WHERE id = 1`).Scan(&config.ServerAddr)
	if err != nil && err != sql.ErrNoRows {
		log.Printf("failed to load server config: %v", err)
		return ServerConfig{}
	}
	ports, err := loadAllowedPorts(s.db)
	if err != nil {
		log.Printf("failed to load allowed ports: %v", err)
		return ServerConfig{ServerAddr: config.ServerAddr}
	}
	config.AllowedPorts = ports
	return config
}

// UpdateServerConfig updates the server configuration (can be modified after initialization).
func (s *AdminStore) UpdateServerConfig(config ServerConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if _, err := tx.Exec(`INSERT OR IGNORE INTO server_config (id) VALUES (1)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE server_config SET server_addr = ? WHERE id = 1`, config.ServerAddr); err != nil {
		return err
	}
	if err := replaceAllowedPorts(tx, config.AllowedPorts); err != nil {
		return err
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

// ========== Port Whitelist ==========

func loadAllowedPorts(q dbQuerier) (ports []PortRange, err error) {
	rows, qerr := q.Query(`SELECT start_port, end_port FROM allowed_ports ORDER BY id`)
	if qerr != nil {
		return nil, qerr
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	for rows.Next() {
		var port PortRange
		if err = rows.Scan(&port.Start, &port.End); err != nil {
			return nil, err
		}
		ports = append(ports, port)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return ports, nil
}

func replaceAllowedPorts(exec dbExecer, ports []PortRange) error {
	if _, err := exec.Exec(`DELETE FROM allowed_ports`); err != nil {
		return err
	}
	for _, port := range ports {
		if _, err := exec.Exec(`INSERT INTO allowed_ports (start_port, end_port) VALUES (?, ?)`, port.Start, port.End); err != nil {
			return err
		}
	}
	return nil
}

// IsPortAllowed checks whether a port is within the allowed range.
// If the allowlist is empty (uninitialized), returns true.
func (s *AdminStore) IsPortAllowed(port int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ports, err := loadAllowedPorts(s.db)
	if err != nil {
		log.Printf("failed to load allowed ports: %v", err)
		return false
	}
	if len(ports) == 0 {
		return true
	}
	for _, pr := range ports {
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

func scanAdminUser(row dbScanner) (AdminUser, error) {
	var user AdminUser
	var createdAt string
	var lastLogin sql.NullString
	if err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &createdAt, &lastLogin); err != nil {
		return AdminUser{}, err
	}
	parsedCreatedAt, err := parseTime(createdAt)
	if err != nil {
		return AdminUser{}, err
	}
	parsedLastLogin, err := parseOptionalTime(lastLogin)
	if err != nil {
		return AdminUser{}, err
	}
	user.CreatedAt = parsedCreatedAt
	user.LastLogin = parsedLastLogin
	return user, nil
}

func (s *AdminStore) ValidateAdminPassword(username, password string) (*AdminUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, err := scanAdminUser(s.db.QueryRow(`SELECT id, username, password_hash, role, created_at, last_login FROM admin_users WHERE username = ?`, username))
	if err == sql.ErrNoRows {
		_ = bcrypt.CompareHashAndPassword(s.getDummyHash(), []byte(password))
		return nil, fmt.Errorf("incorrect username or password")
	}
	if err != nil {
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("incorrect username or password")
	}
	return &user, nil
}

func (s *AdminStore) UpdateAdminLoginTime(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`UPDATE admin_users SET last_login = ? WHERE id = ?`, formatTime(time.Now()), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		committed = true
		return tx.Rollback()
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

// ========== Clients ==========

func scanRegisteredClientBase(row dbScanner) (RegisteredClient, error) {
	var client RegisteredClient
	var createdAt, lastSeen string
	if err := row.Scan(
		&client.ID,
		&client.InstallID,
		&client.DisplayName,
		&client.Info.Hostname,
		&client.Info.OS,
		&client.Info.Arch,
		&client.Info.IP,
		&client.Info.Version,
		&client.Info.PublicIPv4,
		&client.Info.PublicIPv6,
		&client.IngressBPS,
		&client.EgressBPS,
		&createdAt,
		&lastSeen,
		&client.LastIP,
	); err != nil {
		return RegisteredClient{}, err
	}
	parsedCreatedAt, err := parseTime(createdAt)
	if err != nil {
		return RegisteredClient{}, err
	}
	parsedLastSeen, err := parseTime(lastSeen)
	if err != nil {
		return RegisteredClient{}, err
	}
	client.CreatedAt = parsedCreatedAt
	client.LastSeen = parsedLastSeen
	return client, nil
}

func registeredClientSelectColumns() string {
	return `id, install_id, display_name, hostname, os, arch, ip, version, public_ipv4, public_ipv6, ingress_bps, egress_bps, created_at, last_seen, last_ip`
}

func loadRegisteredClient(q dbQuerier, where string, args ...any) (RegisteredClient, error) {
	client, err := scanRegisteredClientBase(q.QueryRow(`SELECT `+registeredClientSelectColumns()+` FROM registered_clients `+where, args...))
	if err != nil {
		return RegisteredClient{}, err
	}
	stats, err := loadClientStats(q, client.ID)
	if err != nil {
		return RegisteredClient{}, err
	}
	client.Stats = stats
	return client, nil
}

func upsertClientInfo(exec dbExecer, clientID string, info protocol.ClientInfo, lastSeen time.Time, lastIP string) error {
	if _, err := exec.Exec(`UPDATE registered_clients
		SET hostname = ?, os = ?, arch = ?, ip = ?, version = ?, public_ipv4 = ?, public_ipv6 = ?, last_seen = ?, last_ip = ?
		WHERE id = ?`,
		info.Hostname, info.OS, info.Arch, info.IP, info.Version, info.PublicIPv4, info.PublicIPv6, formatTime(lastSeen), lastIP, clientID); err != nil {
		return err
	}
	return nil
}

func (s *AdminStore) GetOrCreateClient(installID string, info protocol.ClientInfo, remoteAddr string) (*RegisteredClient, error) {
	if installID == "" {
		return nil, fmt.Errorf("install_id must not be empty")
	}

	lastIP := remoteIP(remoteAddr)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	client, err := loadRegisteredClient(tx, `WHERE install_id = ?`, installID)
	if err == nil {
		client.Info = info
		client.LastSeen = now
		client.LastIP = lastIP
		if err := upsertClientInfo(tx, client.ID, info, now, lastIP); err != nil {
			return nil, err
		}
		if err := s.maybeFailSave(); err != nil {
			return nil, err
		}
		if err := commitTx(tx, &committed); err != nil {
			return nil, err
		}
		client.Stats = cloneSystemStats(client.Stats)
		return &client, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	client = RegisteredClient{
		ID:        generateUUID(),
		InstallID: installID,
		Info:      info,
		CreatedAt: now,
		LastSeen:  now,
		LastIP:    lastIP,
	}
	if _, err := tx.Exec(`INSERT INTO registered_clients
		(id, install_id, display_name, hostname, os, arch, ip, version, public_ipv4, public_ipv6, ingress_bps, egress_bps, created_at, last_seen, last_ip)
		VALUES (?, ?, '', ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?)`,
		client.ID, client.InstallID, info.Hostname, info.OS, info.Arch, info.IP, info.Version, info.PublicIPv4, info.PublicIPv6, formatTime(now), formatTime(now), lastIP); err != nil {
		return nil, err
	}
	if err := s.maybeFailSave(); err != nil {
		return nil, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return nil, err
	}
	return &client, nil
}

func (s *AdminStore) TouchClient(clientID string, info protocol.ClientInfo, remoteAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	lastIP := remoteIP(remoteAddr)
	now := time.Now()
	if lastIP == "" {
		var existing string
		err := s.db.QueryRow(`SELECT last_ip FROM registered_clients WHERE id = ?`, clientID).Scan(&existing)
		if err == sql.ErrNoRows {
			return fmt.Errorf("client %q not found", clientID)
		}
		if err != nil {
			return err
		}
		lastIP = existing
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`UPDATE registered_clients
		SET hostname = ?, os = ?, arch = ?, ip = ?, version = ?, public_ipv4 = ?, public_ipv6 = ?, last_seen = ?, last_ip = ?
		WHERE id = ?`,
		info.Hostname, info.OS, info.Arch, info.IP, info.Version, info.PublicIPv4, info.PublicIPv6, formatTime(now), lastIP, clientID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("client %q not found", clientID)
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
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

func loadClientStats(q dbQuerier, clientID string) (*protocol.SystemStats, error) {
	var stats protocol.SystemStats
	var memTotal, memUsed, diskTotal, diskUsed, netSent, netRecv int64
	var uptime, processUptime, osInstallTime, appMemUsed, appMemSys int64
	var updatedAtRaw, freshUntilRaw sql.NullString
	err := q.QueryRow(`SELECT cpu_usage, mem_total, mem_used, mem_usage, disk_total, disk_used, disk_usage, net_sent, net_recv,
		net_sent_speed, net_recv_speed, uptime, process_uptime, os_install_time, num_cpu, app_mem_used, app_mem_sys,
		public_ipv4, public_ipv6, updated_at, fresh_until
		FROM client_stats WHERE client_id = ?`, clientID).Scan(
		&stats.CPUUsage, &memTotal, &memUsed, &stats.MemUsage, &diskTotal, &diskUsed, &stats.DiskUsage,
		&netSent, &netRecv, &stats.NetSentSpeed, &stats.NetRecvSpeed, &uptime, &processUptime, &osInstallTime,
		&stats.NumCPU, &appMemUsed, &appMemSys, &stats.PublicIPv4, &stats.PublicIPv6, &updatedAtRaw, &freshUntilRaw,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	updatedAt, err := parseOptionalTimeValue(updatedAtRaw)
	if err != nil {
		return nil, err
	}
	freshUntil, err := parseOptionalTimeValue(freshUntilRaw)
	if err != nil {
		return nil, err
	}
	if stats.MemTotal, err = sqliteUint64("client_stats.mem_total", memTotal); err != nil {
		return nil, err
	}
	if stats.MemUsed, err = sqliteUint64("client_stats.mem_used", memUsed); err != nil {
		return nil, err
	}
	if stats.DiskTotal, err = sqliteUint64("client_stats.disk_total", diskTotal); err != nil {
		return nil, err
	}
	if stats.DiskUsed, err = sqliteUint64("client_stats.disk_used", diskUsed); err != nil {
		return nil, err
	}
	if stats.NetSent, err = sqliteUint64("client_stats.net_sent", netSent); err != nil {
		return nil, err
	}
	if stats.NetRecv, err = sqliteUint64("client_stats.net_recv", netRecv); err != nil {
		return nil, err
	}
	if stats.Uptime, err = sqliteUint64("client_stats.uptime", uptime); err != nil {
		return nil, err
	}
	if stats.ProcessUptime, err = sqliteUint64("client_stats.process_uptime", processUptime); err != nil {
		return nil, err
	}
	if stats.OSInstallTime, err = sqliteUint64("client_stats.os_install_time", osInstallTime); err != nil {
		return nil, err
	}
	if stats.AppMemUsed, err = sqliteUint64("client_stats.app_mem_used", appMemUsed); err != nil {
		return nil, err
	}
	if stats.AppMemSys, err = sqliteUint64("client_stats.app_mem_sys", appMemSys); err != nil {
		return nil, err
	}
	stats.UpdatedAt = updatedAt
	stats.FreshUntil = freshUntil

	partitions, err := loadClientDiskPartitions(q, clientID)
	if err != nil {
		return nil, err
	}
	stats.DiskPartitions = partitions
	return &stats, nil
}

func loadClientDiskPartitions(q dbQuerier, clientID string) (partitions []protocol.DiskPartition, err error) {
	rows, qerr := q.Query(`SELECT path, used, total FROM client_disk_partitions WHERE client_id = ? ORDER BY path`, clientID)
	if qerr != nil {
		return nil, qerr
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	for rows.Next() {
		var partition protocol.DiskPartition
		var used, total int64
		if err = rows.Scan(&partition.Path, &used, &total); err != nil {
			return nil, err
		}
		partition.Used, err = sqliteUint64("client_disk_partitions.used", used)
		if err != nil {
			return nil, err
		}
		partition.Total, err = sqliteUint64("client_disk_partitions.total", total)
		if err != nil {
			return nil, err
		}
		partitions = append(partitions, partition)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return partitions, nil
}

func replaceClientStats(exec dbExecer, clientID string, stats protocol.SystemStats) error {
	memTotal, err := sqliteInt64("client_stats.mem_total", stats.MemTotal)
	if err != nil {
		return err
	}
	memUsed, err := sqliteInt64("client_stats.mem_used", stats.MemUsed)
	if err != nil {
		return err
	}
	diskTotal, err := sqliteInt64("client_stats.disk_total", stats.DiskTotal)
	if err != nil {
		return err
	}
	diskUsed, err := sqliteInt64("client_stats.disk_used", stats.DiskUsed)
	if err != nil {
		return err
	}
	netSent, err := sqliteInt64("client_stats.net_sent", stats.NetSent)
	if err != nil {
		return err
	}
	netRecv, err := sqliteInt64("client_stats.net_recv", stats.NetRecv)
	if err != nil {
		return err
	}
	uptime, err := sqliteInt64("client_stats.uptime", stats.Uptime)
	if err != nil {
		return err
	}
	processUptime, err := sqliteInt64("client_stats.process_uptime", stats.ProcessUptime)
	if err != nil {
		return err
	}
	osInstallTime, err := sqliteInt64("client_stats.os_install_time", stats.OSInstallTime)
	if err != nil {
		return err
	}
	appMemUsed, err := sqliteInt64("client_stats.app_mem_used", stats.AppMemUsed)
	if err != nil {
		return err
	}
	appMemSys, err := sqliteInt64("client_stats.app_mem_sys", stats.AppMemSys)
	if err != nil {
		return err
	}

	if _, err := exec.Exec(`INSERT INTO client_stats
		(client_id, cpu_usage, mem_total, mem_used, mem_usage, disk_total, disk_used, disk_usage, net_sent, net_recv,
		 net_sent_speed, net_recv_speed, uptime, process_uptime, os_install_time, num_cpu, app_mem_used, app_mem_sys,
		 public_ipv4, public_ipv6, updated_at, fresh_until)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(client_id) DO UPDATE SET
		 cpu_usage = excluded.cpu_usage,
		 mem_total = excluded.mem_total,
		 mem_used = excluded.mem_used,
		 mem_usage = excluded.mem_usage,
		 disk_total = excluded.disk_total,
		 disk_used = excluded.disk_used,
		 disk_usage = excluded.disk_usage,
		 net_sent = excluded.net_sent,
		 net_recv = excluded.net_recv,
		 net_sent_speed = excluded.net_sent_speed,
		 net_recv_speed = excluded.net_recv_speed,
		 uptime = excluded.uptime,
		 process_uptime = excluded.process_uptime,
		 os_install_time = excluded.os_install_time,
		 num_cpu = excluded.num_cpu,
		 app_mem_used = excluded.app_mem_used,
		 app_mem_sys = excluded.app_mem_sys,
		 public_ipv4 = excluded.public_ipv4,
		 public_ipv6 = excluded.public_ipv6,
		 updated_at = excluded.updated_at,
		 fresh_until = excluded.fresh_until`,
		clientID, stats.CPUUsage, memTotal, memUsed, stats.MemUsage, diskTotal, diskUsed,
		stats.DiskUsage, netSent, netRecv, stats.NetSentSpeed, stats.NetRecvSpeed, uptime,
		processUptime, osInstallTime, stats.NumCPU, appMemUsed, appMemSys,
		stats.PublicIPv4, stats.PublicIPv6, nullableTimeValue(stats.UpdatedAt), nullableTimeValue(stats.FreshUntil)); err != nil {
		return err
	}
	if _, err := exec.Exec(`DELETE FROM client_disk_partitions WHERE client_id = ?`, clientID); err != nil {
		return err
	}
	for _, partition := range stats.DiskPartitions {
		partitionUsed, err := sqliteInt64("client_disk_partitions.used", partition.Used)
		if err != nil {
			return err
		}
		partitionTotal, err := sqliteInt64("client_disk_partitions.total", partition.Total)
		if err != nil {
			return err
		}
		if _, err := exec.Exec(`INSERT INTO client_disk_partitions (client_id, path, used, total) VALUES (?, ?, ?, ?)`,
			clientID, partition.Path, partitionUsed, partitionTotal); err != nil {
			return err
		}
	}
	return nil
}

func (s *AdminStore) UpdateClientStats(clientID string, info protocol.ClientInfo, stats protocol.SystemStats, remoteAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	lastIP := remoteIP(remoteAddr)
	now := time.Now()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if lastIP == "" {
		err := tx.QueryRow(`SELECT last_ip FROM registered_clients WHERE id = ?`, clientID).Scan(&lastIP)
		if err == sql.ErrNoRows {
			return fmt.Errorf("client %q not found", clientID)
		}
		if err != nil {
			return err
		}
	}
	result, err := tx.Exec(`UPDATE registered_clients
		SET hostname = ?, os = ?, arch = ?, ip = ?, version = ?, public_ipv4 = ?, public_ipv6 = ?, last_seen = ?, last_ip = ?
		WHERE id = ?`,
		info.Hostname, info.OS, info.Arch, info.IP, info.Version, info.PublicIPv4, info.PublicIPv6, formatTime(now), lastIP, clientID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("client %q not found", clientID)
	}
	if err := replaceClientStats(tx, clientID, stats); err != nil {
		return err
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

func (s *AdminStore) GetRegisteredClients() []RegisteredClient {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT ` + registeredClientSelectColumns() + ` FROM registered_clients ORDER BY created_at, id`)
	if err != nil {
		log.Printf("failed to list registered clients: %v", err)
		return nil
	}

	var clients []RegisteredClient
	for rows.Next() {
		client, err := scanRegisteredClientBase(rows)
		if err != nil {
			_ = rows.Close()
			log.Printf("failed to scan registered client: %v", err)
			return nil
		}
		clients = append(clients, client)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		log.Printf("failed to list registered clients: %v", err)
		return nil
	}
	if err := rows.Close(); err != nil {
		log.Printf("failed to close registered clients rows: %v", err)
		return nil
	}

	for i := range clients {
		stats, err := loadClientStats(s.db, clients[i].ID)
		if err != nil {
			log.Printf("failed to load client stats: %v", err)
			return nil
		}
		clients[i].Stats = cloneSystemStats(stats)
	}
	return clients
}

func (s *AdminStore) GetRegisteredClient(clientID string) (RegisteredClient, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	client, err := loadRegisteredClient(s.db, `WHERE id = ?`, clientID)
	if err == sql.ErrNoRows {
		return RegisteredClient{}, false
	}
	if err != nil {
		log.Printf("failed to get registered client %q: %v", clientID, err)
		return RegisteredClient{}, false
	}
	client.Stats = cloneSystemStats(client.Stats)
	return client, true
}

func registeredClientBandwidthSettings(client RegisteredClient) protocol.BandwidthSettings {
	return protocol.BandwidthSettings{
		IngressBPS: client.IngressBPS,
		EgressBPS:  client.EgressBPS,
	}
}

func (s *AdminStore) UpdateClientBandwidthSettings(clientID string, settings protocol.BandwidthSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`UPDATE registered_clients SET ingress_bps = ?, egress_bps = ? WHERE id = ?`, settings.IngressBPS, settings.EgressBPS, clientID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrRegisteredClientNotFound
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

// ========== Display Name ==========

// UpdateClientDisplayName updates the custom display name for a Client.
func (s *AdminStore) UpdateClientDisplayName(clientID, displayName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`UPDATE registered_clients SET display_name = ? WHERE id = ?`, displayName, clientID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("client %q not found", clientID)
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

// ========== Sessions ==========

func scanAdminSession(row dbScanner) (AdminSession, error) {
	var session AdminSession
	var createdAt, expiresAt string
	if err := row.Scan(&session.ID, &session.UserID, &session.Username, &session.Role, &createdAt, &expiresAt, &session.IP, &session.UserAgent); err != nil {
		return AdminSession{}, err
	}
	parsedCreatedAt, err := parseTime(createdAt)
	if err != nil {
		return AdminSession{}, err
	}
	parsedExpiresAt, err := parseTime(expiresAt)
	if err != nil {
		return AdminSession{}, err
	}
	session.CreatedAt = parsedCreatedAt
	session.ExpiresAt = parsedExpiresAt
	return session, nil
}

// CreateSession creates a new session (removes existing sessions for the same user → single active session).
func (s *AdminStore) CreateSession(userID, username, role, ip, ua string) (*AdminSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	session := AdminSession{
		ID:        generateUUID(),
		UserID:    userID,
		Username:  username,
		Role:      role,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionDefaultTTL),
		IP:        ip,
		UserAgent: ua,
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO admin_sessions (id, user_id, username, role, created_at, expires_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, session.ID, session.UserID, session.Username, session.Role, formatTime(session.CreatedAt), formatTime(session.ExpiresAt), session.IP, session.UserAgent); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE admin_users SET last_login = ? WHERE id = ?`, formatTime(time.Now()), userID); err != nil {
		return nil, err
	}
	if err := s.maybeFailSave(); err != nil {
		return nil, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return nil, err
	}
	return &session, nil
}

// GetSession retrieves the specified session (returns nil if not found or expired).
func (s *AdminStore) GetSession(sessionID string) *AdminSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, err := scanAdminSession(s.db.QueryRow(`SELECT id, user_id, username, role, created_at, expires_at, ip, user_agent FROM admin_sessions WHERE id = ?`, sessionID))
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		log.Printf("failed to get admin session %q: %v", sessionID, err)
		return nil
	}
	if time.Now().After(session.ExpiresAt) {
		return nil
	}
	return &session
}

// DeleteSession removes the specified session.
func (s *AdminStore) DeleteSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE id = ?`, sessionID); err != nil {
		return err
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

// DeleteSessionsByUserID removes all sessions belonging to the specified user.
func (s *AdminStore) DeleteSessionsByUserID(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

// CleanExpiredSessions removes all expired sessions.
func (s *AdminStore) CleanExpiredSessions() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := formatTime(time.Now())
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`DELETE FROM admin_sessions WHERE expires_at <= ?`, now)
	if err != nil {
		return err
	}
	cleaned, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if cleaned == 0 {
		committed = true
		return tx.Rollback()
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	if err := commitTx(tx, &committed); err != nil {
		return err
	}
	log.Printf("🧹 Cleaned up %d expired sessions", cleaned)
	return nil
}

// ========== API Keys ==========

func scanAPIKeyBase(row dbScanner) (APIKey, error) {
	var key APIKey
	var createdAt string
	var expiresAt sql.NullString
	var isActive int
	if err := row.Scan(&key.ID, &key.Name, &key.KeyHash, &createdAt, &expiresAt, &isActive, &key.MaxUses, &key.UseCount); err != nil {
		return APIKey{}, err
	}
	parsedCreatedAt, err := parseTime(createdAt)
	if err != nil {
		return APIKey{}, err
	}
	parsedExpiresAt, err := parseOptionalTime(expiresAt)
	if err != nil {
		return APIKey{}, err
	}
	key.CreatedAt = parsedCreatedAt
	key.ExpiresAt = parsedExpiresAt
	key.IsActive = intToBool(isActive)
	return key, nil
}

func apiKeySelectColumns() string {
	return `id, name, key_hash, created_at, expires_at, is_active, max_uses, use_count`
}

func loadAPIKeyPermissions(q dbQuerier, keyID string) (permissions []string, err error) {
	rows, qerr := q.Query(`SELECT permission FROM api_key_permissions WHERE api_key_id = ? ORDER BY permission`, keyID)
	if qerr != nil {
		return nil, qerr
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	for rows.Next() {
		var permission string
		if err = rows.Scan(&permission); err != nil {
			return nil, err
		}
		permissions = append(permissions, permission)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return permissions, nil
}

func loadAPIKeys(q dbQuerier) ([]APIKey, error) {
	rows, err := q.Query(`SELECT ` + apiKeySelectColumns() + ` FROM api_keys ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}

	var keys []APIKey
	for rows.Next() {
		key, err := scanAPIKeyBase(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	for i := range keys {
		permissions, err := loadAPIKeyPermissions(q, keys[i].ID)
		if err != nil {
			return nil, err
		}
		keys[i].Permissions = permissions
	}
	return keys, nil
}

func insertAPIKey(exec dbExecer, key APIKey) error {
	if _, err := exec.Exec(`INSERT INTO api_keys (id, name, key_hash, created_at, expires_at, is_active, max_uses, use_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		key.ID, key.Name, key.KeyHash, formatTime(key.CreatedAt), optionalTimePtrValue(key.ExpiresAt), boolToInt(key.IsActive), key.MaxUses, key.UseCount); err != nil {
		return err
	}
	for _, permission := range key.Permissions {
		if _, err := exec.Exec(`INSERT INTO api_key_permissions (api_key_id, permission) VALUES (?, ?)`, key.ID, permission); err != nil {
			return err
		}
	}
	return nil
}

func optionalTimePtrValue(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

// ValidateClientKey checks whether the given key exists, is enabled, and has not expired.
// This only validates the key — it does not consume a use count (that happens in ExchangeToken).
func (s *AdminStore) ValidateClientKey(key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateClientKeyLocked(s.db, key)
}

// validateClientKeyLocked is an internal method; caller must already hold mu.
func (s *AdminStore) validateClientKeyLocked(q dbQuerier, key string) (bool, error) {
	keys, err := loadAPIKeys(q)
	if err != nil {
		return false, err
	}
	if len(keys) == 0 {
		initialized, _, err := s.loadConfigLifecycle(q)
		if err != nil {
			return false, err
		}
		if !initialized {
			return false, fmt.Errorf("server not initialized; client connections are not accepted yet")
		}
		return false, fmt.Errorf("no API keys configured")
	}

	if key == "" {
		return false, fmt.Errorf("no valid API key provided and authentication is required")
	}

	for _, k := range keys {
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

// findKeyByRaw returns the matching key, if found. Caller must hold mu.
func (s *AdminStore) findKeyByRaw(q dbQuerier, key string) (*APIKey, error) {
	keys, err := loadAPIKeys(q)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if err := bcrypt.CompareHashAndPassword([]byte(k.KeyHash), []byte(key)); err == nil {
			copy := k
			return &copy, nil
		}
	}
	return nil, nil
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

func scanClientToken(row dbScanner) (ClientToken, error) {
	var token ClientToken
	var createdAt, lastActiveAt string
	var revoked int
	if err := row.Scan(&token.ID, &token.TokenHash, &token.InstallID, &token.KeyID, &token.ClientID, &createdAt, &lastActiveAt, &token.LastIP, &revoked); err != nil {
		return ClientToken{}, err
	}
	parsedCreatedAt, err := parseTime(createdAt)
	if err != nil {
		return ClientToken{}, err
	}
	parsedLastActiveAt, err := parseTime(lastActiveAt)
	if err != nil {
		return ClientToken{}, err
	}
	token.CreatedAt = parsedCreatedAt
	token.LastActiveAt = parsedLastActiveAt
	token.IsRevoked = intToBool(revoked)
	return token, nil
}

func clientTokenSelectColumns() string {
	return `id, token_hash, install_id, key_id, client_id, created_at, last_active_at, last_ip, is_revoked`
}

func loadClientTokens(q dbQuerier, where string, args ...any) ([]ClientToken, error) {
	rows, err := q.Query(`SELECT `+clientTokenSelectColumns()+` FROM client_tokens `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []ClientToken
	for rows.Next() {
		token, err := scanClientToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// ExchangeToken exchanges a Key for a client token.
// If the given install_id already has a valid token, the existing token is refreshed
// and returned without consuming a key use count.
// Otherwise the key is validated, a use count is consumed, and a new token is issued.
func (s *AdminStore) ExchangeToken(key, installID, clientID, remoteAddr string) (string, *ClientToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ip := remoteIP(remoteAddr)
	now := time.Now()

	tx, err := s.db.Begin()
	if err != nil {
		return "", nil, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	tokens, err := loadClientTokens(tx, `WHERE install_id = ? AND is_revoked = 0 ORDER BY created_at, id`, installID)
	if err != nil {
		return "", nil, err
	}
	for _, t := range tokens {
		if now.Sub(t.LastActiveAt) < tokenExpiryDuration {
			newToken, err := generateToken()
			if err != nil {
				return "", nil, err
			}
			t.TokenHash = hashToken(newToken)
			t.LastActiveAt = now
			t.LastIP = ip
			t.ClientID = clientID
			if _, err := tx.Exec(`UPDATE client_tokens SET token_hash = ?, last_active_at = ?, last_ip = ?, client_id = ? WHERE id = ?`,
				t.TokenHash, formatTime(t.LastActiveAt), t.LastIP, t.ClientID, t.ID); err != nil {
				return "", nil, err
			}
			if err := s.maybeFailSave(); err != nil {
				return "", nil, err
			}
			if err := commitTx(tx, &committed); err != nil {
				return "", nil, err
			}
			log.Printf("🔑 Token refreshed [install_id=%s]: reused existing valid token without consuming key", installID)
			return newToken, &t, nil
		}
	}

	valid, err := s.validateClientKeyLocked(tx, key)
	if !valid {
		return "", nil, fmt.Errorf("key validation failed: %w", err)
	}
	apiKey, err := s.findKeyByRaw(tx, key)
	if err != nil {
		return "", nil, err
	}

	newToken, err := generateToken()
	if err != nil {
		return "", nil, err
	}

	keyID := ""
	if apiKey != nil {
		keyID = apiKey.ID
		if _, err := tx.Exec(`UPDATE api_keys SET use_count = use_count + 1 WHERE id = ?`, apiKey.ID); err != nil {
			return "", nil, err
		}
	}

	clientToken := ClientToken{
		ID:           generateUUID(),
		TokenHash:    hashToken(newToken),
		InstallID:    installID,
		KeyID:        keyID,
		ClientID:     clientID,
		CreatedAt:    now,
		LastActiveAt: now,
		LastIP:       ip,
	}

	if _, err := tx.Exec(`UPDATE client_tokens SET is_revoked = 1 WHERE install_id = ? AND is_revoked = 0`, installID); err != nil {
		return "", nil, err
	}
	if _, err := tx.Exec(`INSERT INTO client_tokens (id, token_hash, install_id, key_id, client_id, created_at, last_active_at, last_ip, is_revoked)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		clientToken.ID, clientToken.TokenHash, clientToken.InstallID, clientToken.KeyID, clientToken.ClientID,
		formatTime(clientToken.CreatedAt), formatTime(clientToken.LastActiveAt), clientToken.LastIP); err != nil {
		return "", nil, err
	}
	if err := s.maybeFailSave(); err != nil {
		return "", nil, err
	}
	if err := commitTx(tx, &committed); err != nil {
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
	now := time.Now()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	tokens, err := loadClientTokens(tx, `ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	for _, t := range tokens {
		if subtle.ConstantTimeCompare([]byte(t.TokenHash), []byte(tokenHash)) == 1 {
			if t.IsRevoked {
				return nil, ErrClientTokenRevoked
			}
			if t.InstallID != installID {
				log.Printf("⚠️ Token install_id mismatch: token_install=%s, req_install=%s", t.InstallID, installID)
				return nil, ErrClientTokenInstallMismatch
			}
			if now.Sub(t.LastActiveAt) >= tokenExpiryDuration {
				return nil, ErrClientTokenExpired
			}
			t.LastActiveAt = now
			if _, err := tx.Exec(`UPDATE client_tokens SET last_active_at = ? WHERE id = ?`, formatTime(t.LastActiveAt), t.ID); err != nil {
				return nil, err
			}
			if err := s.maybeFailSave(); err != nil {
				return nil, err
			}
			if err := commitTx(tx, &committed); err != nil {
				return nil, err
			}
			return &t, nil
		}
	}

	return nil, ErrClientTokenInvalid
}

// TouchToken updates the last-active time and IP address for the specified token.
func (s *AdminStore) TouchToken(tokenID, remoteAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	ip := remoteIP(remoteAddr)

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	var result sql.Result
	if ip == "" {
		result, err = tx.Exec(`UPDATE client_tokens SET last_active_at = ? WHERE id = ?`, formatTime(now), tokenID)
	} else {
		result, err = tx.Exec(`UPDATE client_tokens SET last_active_at = ?, last_ip = ? WHERE id = ?`, formatTime(now), ip, tokenID)
	}
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		committed = true
		return tx.Rollback()
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

// RevokeToken revokes the specified token.
func (s *AdminStore) RevokeToken(tokenID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`UPDATE client_tokens SET is_revoked = 1 WHERE id = ?`, tokenID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("token %q not found", tokenID)
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

// RevokeTokensByKeyID revokes all tokens associated with the given key ID, returning the count revoked.
func (s *AdminStore) RevokeTokensByKeyID(keyID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`UPDATE client_tokens SET is_revoked = 1 WHERE key_id = ? AND is_revoked = 0`, keyID)
	if err != nil {
		return 0, err
	}
	count64, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if count64 == 0 {
		committed = true
		if err := tx.Rollback(); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if err := s.maybeFailSave(); err != nil {
		return 0, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, err
	}
	return int(count64), nil
}

// CleanExpiredTokens removes tokens that have been inactive for more than 7 days and revoked tokens.
func (s *AdminStore) CleanExpiredTokens() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := formatTime(time.Now().Add(-tokenExpiryDuration))
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`DELETE FROM client_tokens WHERE is_revoked = 1 OR last_active_at <= ?`, cutoff)
	if err != nil {
		return err
	}
	cleaned, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if cleaned == 0 {
		committed = true
		return tx.Rollback()
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	if err := commitTx(tx, &committed); err != nil {
		return err
	}
	log.Printf("🧹 Cleaned up %d expired/revoked tokens", cleaned)
	return nil
}

// GetTokensByKeyID returns all tokens exchanged by a given key.
func (s *AdminStore) GetTokensByKeyID(keyID string) []ClientToken {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tokens, err := loadClientTokens(s.db, `WHERE key_id = ? ORDER BY created_at, id`, keyID)
	if err != nil {
		log.Printf("failed to get tokens by key %q: %v", keyID, err)
		return nil
	}
	return tokens
}

// GetClientTokenByInstallID returns the active token for a given install_id, if any.
func (s *AdminStore) GetClientTokenByInstallID(installID string) *ClientToken {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tokens, err := loadClientTokens(s.db, `WHERE install_id = ? AND is_revoked = 0 ORDER BY created_at, id`, installID)
	if err != nil {
		log.Printf("failed to get client token for install %q: %v", installID, err)
		return nil
	}
	now := time.Now()
	for _, token := range tokens {
		if now.Sub(token.LastActiveAt) < tokenExpiryDuration {
			copy := token
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

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if err := insertAPIKey(tx, k); err != nil {
		return nil, err
	}
	if err := s.maybeFailSave(); err != nil {
		return nil, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return nil, err
	}

	return &k, nil
}

func (s *AdminStore) GetAPIKeys() []APIKey {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys, err := loadAPIKeys(s.db)
	if err != nil {
		log.Printf("failed to list API keys: %v", err)
		return nil
	}
	return keys
}

func (s *AdminStore) SetAPIKeyActive(id string, active bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`UPDATE api_keys SET is_active = ? WHERE id = ?`, boolToInt(active), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("API key %q not found", id)
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

func (s *AdminStore) DeleteAPIKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("API key %q not found", id)
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

// SetAPIKeyMaxUses sets the maximum use count for an API key.
func (s *AdminStore) SetAPIKeyMaxUses(id string, maxUses int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`UPDATE api_keys SET max_uses = ? WHERE id = ?`, maxUses, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("API key %q not found", id)
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
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
