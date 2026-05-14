package run

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jungju/jj/internal/artifact"
	"github.com/jungju/jj/internal/security"
	_ "modernc.org/sqlite"
)

const WorkspaceStateDBPath = artifact.DocumentsDBRel

const legacyWorkspaceStateDBPath = ".jj/documents.sqlite3"

var workspaceStateDBMu sync.Mutex

const workspaceStateSchema = `
CREATE TABLE IF NOT EXISTS workspace_spec (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	version INTEGER NOT NULL,
	title TEXT NOT NULL,
	summary TEXT NOT NULL,
	goals_json TEXT NOT NULL,
	non_goals_json TEXT NOT NULL,
	requirements_json TEXT NOT NULL,
	acceptance_criteria_json TEXT NOT NULL,
	open_questions_json TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	sha256 TEXT NOT NULL,
	bytes INTEGER NOT NULL,
	stored_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workspace_task_meta (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	version INTEGER NOT NULL,
	active_task_id TEXT,
	sha256 TEXT NOT NULL,
	bytes INTEGER NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workspace_tasks (
	seq INTEGER PRIMARY KEY,
	task_id TEXT NOT NULL,
	title TEXT NOT NULL,
	mode TEXT NOT NULL,
	selected_task_proposal_mode TEXT NOT NULL,
	resolved_task_proposal_mode TEXT NOT NULL,
	priority TEXT NOT NULL,
	status TEXT NOT NULL,
	reason TEXT NOT NULL,
	acceptance_criteria_json TEXT NOT NULL,
	validation_command TEXT NOT NULL,
	work_branch TEXT NOT NULL,
	next_intent_hash TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	created_by_run TEXT NOT NULL,
	created_by_turn TEXT,
	completed_by_run TEXT,
	completed_by_turn TEXT,
	commit_hash TEXT,
	verdict TEXT
);

CREATE INDEX IF NOT EXISTS workspace_tasks_task_id_idx ON workspace_tasks(task_id);
CREATE INDEX IF NOT EXISTS workspace_tasks_status_idx ON workspace_tasks(status, seq);
`

func IsWorkspaceStatePath(rel string) bool {
	clean, err := cleanWorkspaceStateRel(rel)
	if err != nil {
		return false
	}
	return clean == DefaultSpecStatePath || clean == DefaultTasksStatePath
}

func ReadWorkspaceStateDocument(cwd, rel string) ([]byte, bool, error) {
	clean, err := cleanWorkspaceStateRel(rel)
	if err != nil {
		return nil, false, err
	}
	switch clean {
	case DefaultSpecStatePath:
		state, ok, err := loadSpecStateFromStore(cwd)
		if err != nil || !ok {
			return nil, ok, err
		}
		data, err := marshalWorkspaceJSON(state)
		return data, err == nil, err
	case DefaultTasksStatePath:
		state, ok, err := loadTaskStateFromStore(cwd)
		if err != nil || !ok {
			return nil, ok, err
		}
		data, err := marshalWorkspaceJSON(state)
		return data, err == nil, err
	default:
		return nil, false, errors.New("workspace state path is not supported")
	}
}

func WorkspaceStateDocumentAvailable(cwd, rel string) (bool, error) {
	_, ok, err := ReadWorkspaceStateDocument(cwd, rel)
	return ok, err
}

func loadSpecStateFromStore(cwd string) (SpecState, bool, error) {
	state, ok, err := readSpecStateFromDB(cwd)
	if err != nil || ok {
		ensureSpecDefaults(&state)
		return state, ok, err
	}
	state, ok, err = readLegacySpecStateFromDB(cwd)
	if err != nil || ok {
		ensureSpecDefaults(&state)
		if ok {
			_ = writeSpecStateToDB(cwd, state)
		}
		return state, ok, err
	}
	ok, err = readLegacyWorkspaceJSON(cwd, DefaultSpecStatePath, &state)
	if err != nil || !ok {
		ensureSpecDefaults(&state)
		return state, ok, err
	}
	ensureSpecDefaults(&state)
	_ = writeSpecStateToDB(cwd, state)
	return state, true, nil
}

func loadTaskStateFromStore(cwd string) (TaskState, bool, error) {
	state, ok, err := readTaskStateFromDB(cwd)
	if err != nil || ok {
		ensureTaskDefaults(&state)
		return state, ok, err
	}
	state, ok, err = readLegacyTaskStateFromDB(cwd)
	if err != nil || ok {
		ensureTaskDefaults(&state)
		if ok {
			_ = writeTaskStateToDB(cwd, state)
		}
		return state, ok, err
	}
	ok, err = readLegacyWorkspaceJSON(cwd, DefaultTasksStatePath, &state)
	if err != nil || !ok {
		ensureTaskDefaults(&state)
		return state, ok, err
	}
	ensureTaskDefaults(&state)
	_ = writeTaskStateToDB(cwd, state)
	return state, true, nil
}

func writeWorkspaceStateDocument(cwd, rel string, value any) ([]byte, error) {
	clean, err := cleanWorkspaceStateRel(rel)
	if err != nil {
		return nil, err
	}
	data, err := marshalWorkspaceJSON(value)
	if err != nil {
		return nil, err
	}
	switch clean {
	case DefaultSpecStatePath:
		var state SpecState
		if err := json.Unmarshal(data, &state); err != nil {
			return nil, err
		}
		if err := writeSpecStateToDB(cwd, state); err != nil {
			return nil, err
		}
	case DefaultTasksStatePath:
		var state TaskState
		if err := json.Unmarshal(data, &state); err != nil {
			return nil, err
		}
		if err := writeTaskStateToDB(cwd, state); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("workspace state path is not supported")
	}
	_ = removeLegacyWorkspaceStateFile(cwd, clean)
	return data, nil
}

func readSpecStateFromDB(cwd string) (SpecState, bool, error) {
	return readSpecStateFromDBRel(cwd, WorkspaceStateDBPath, false)
}

func readLegacySpecStateFromDB(cwd string) (SpecState, bool, error) {
	return readSpecStateFromDBRel(cwd, legacyWorkspaceStateDBPath, false)
}

func readSpecStateFromDBRel(cwd, dbRel string, create bool) (SpecState, bool, error) {
	var state SpecState
	err := withWorkspaceStateDBRel(cwd, dbRel, create, func(db *sql.DB) error {
		var goals, nonGoals, requirements, acceptance, questions string
		err := db.QueryRowContext(
			context.Background(),
			`SELECT version, title, summary, goals_json, non_goals_json, requirements_json,
				acceptance_criteria_json, open_questions_json, created_at, updated_at
			FROM workspace_spec WHERE id = 1`,
		).Scan(
			&state.Version,
			&state.Title,
			&state.Summary,
			&goals,
			&nonGoals,
			&requirements,
			&acceptance,
			&questions,
			&state.CreatedAt,
			&state.UpdatedAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return errWorkspaceStateNotFound
		}
		if isMissingWorkspaceStateTableError(err) {
			return errWorkspaceStateNotFound
		}
		if err != nil {
			return err
		}
		if err := decodeStringSlice(goals, &state.Goals); err != nil {
			return err
		}
		if err := decodeStringSlice(nonGoals, &state.NonGoals); err != nil {
			return err
		}
		if err := decodeStringSlice(requirements, &state.Requirements); err != nil {
			return err
		}
		if err := decodeStringSlice(acceptance, &state.AcceptanceCriteria); err != nil {
			return err
		}
		return decodeStringSlice(questions, &state.OpenQuestions)
	})
	if errors.Is(err, errWorkspaceStateNotFound) {
		return SpecState{}, false, nil
	}
	if err != nil {
		return SpecState{}, false, err
	}
	return state, true, nil
}

func readTaskStateFromDB(cwd string) (TaskState, bool, error) {
	return readTaskStateFromDBRel(cwd, WorkspaceStateDBPath, false)
}

func readLegacyTaskStateFromDB(cwd string) (TaskState, bool, error) {
	return readTaskStateFromDBRel(cwd, legacyWorkspaceStateDBPath, false)
}

func readTaskStateFromDBRel(cwd, dbRel string, create bool) (TaskState, bool, error) {
	var state TaskState
	err := withWorkspaceStateDBRel(cwd, dbRel, create, func(db *sql.DB) error {
		var active sql.NullString
		err := db.QueryRowContext(
			context.Background(),
			`SELECT version, active_task_id FROM workspace_task_meta WHERE id = 1`,
		).Scan(&state.Version, &active)
		if errors.Is(err, sql.ErrNoRows) {
			return errWorkspaceStateNotFound
		}
		if isMissingWorkspaceStateTableError(err) {
			return errWorkspaceStateNotFound
		}
		if err != nil {
			return err
		}
		if active.Valid {
			state.ActiveTaskID = stringPtr(active.String)
		}
		rows, err := db.QueryContext(
			context.Background(),
			`SELECT task_id, title, mode, selected_task_proposal_mode, resolved_task_proposal_mode,
				priority, status, reason, acceptance_criteria_json, validation_command, work_branch,
				next_intent_hash, created_at, updated_at, created_by_run, created_by_turn,
				completed_by_run, completed_by_turn, commit_hash, verdict
			FROM workspace_tasks ORDER BY seq`,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var task TaskRecord
			var criteria string
			var createdByTurn, completedByRun, completedByTurn, commit, verdict sql.NullString
			if err := rows.Scan(
				&task.ID,
				&task.Title,
				&task.Mode,
				&task.SelectedTaskProposalMode,
				&task.ResolvedTaskProposalMode,
				&task.Priority,
				&task.Status,
				&task.Reason,
				&criteria,
				&task.ValidationCommand,
				&task.WorkBranch,
				&task.NextIntentHash,
				&task.CreatedAt,
				&task.UpdatedAt,
				&task.CreatedByRun,
				&createdByTurn,
				&completedByRun,
				&completedByTurn,
				&commit,
				&verdict,
			); err != nil {
				return err
			}
			if err := decodeStringSlice(criteria, &task.AcceptanceCriteria); err != nil {
				return err
			}
			task.CreatedByTurn = stringPtrFromNull(createdByTurn)
			task.CompletedByRun = stringPtrFromNull(completedByRun)
			task.CompletedByTurn = stringPtrFromNull(completedByTurn)
			task.Commit = stringPtrFromNull(commit)
			task.Verdict = stringPtrFromNull(verdict)
			state.Tasks = append(state.Tasks, task)
		}
		return rows.Err()
	})
	if errors.Is(err, errWorkspaceStateNotFound) {
		return TaskState{}, false, nil
	}
	if err != nil {
		return TaskState{}, false, err
	}
	return state, true, nil
}

func writeSpecStateToDB(cwd string, state SpecState) error {
	ensureSpecDefaults(&state)
	goals, err := encodeStringSlice(state.Goals)
	if err != nil {
		return err
	}
	nonGoals, err := encodeStringSlice(state.NonGoals)
	if err != nil {
		return err
	}
	requirements, err := encodeStringSlice(state.Requirements)
	if err != nil {
		return err
	}
	acceptance, err := encodeStringSlice(state.AcceptanceCriteria)
	if err != nil {
		return err
	}
	questions, err := encodeStringSlice(state.OpenQuestions)
	if err != nil {
		return err
	}
	data, err := marshalWorkspaceJSON(state)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	storedAt := time.Now().UTC().Format(time.RFC3339Nano)
	return withWorkspaceStateDB(cwd, func(db *sql.DB) error {
		_, err := db.ExecContext(
			context.Background(),
			`INSERT INTO workspace_spec (
				id, version, title, summary, goals_json, non_goals_json, requirements_json,
				acceptance_criteria_json, open_questions_json, created_at, updated_at,
				sha256, bytes, stored_at
			) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				version = excluded.version,
				title = excluded.title,
				summary = excluded.summary,
				goals_json = excluded.goals_json,
				non_goals_json = excluded.non_goals_json,
				requirements_json = excluded.requirements_json,
				acceptance_criteria_json = excluded.acceptance_criteria_json,
				open_questions_json = excluded.open_questions_json,
				created_at = excluded.created_at,
				updated_at = excluded.updated_at,
				sha256 = excluded.sha256,
				bytes = excluded.bytes,
				stored_at = excluded.stored_at`,
			state.Version,
			state.Title,
			state.Summary,
			goals,
			nonGoals,
			requirements,
			acceptance,
			questions,
			state.CreatedAt,
			state.UpdatedAt,
			hex.EncodeToString(sum[:]),
			int64(len(data)),
			storedAt,
		)
		return err
	})
}

func writeTaskStateToDB(cwd string, state TaskState) error {
	ensureTaskDefaults(&state)
	data, err := marshalWorkspaceJSON(state)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	storedAt := time.Now().UTC().Format(time.RFC3339Nano)
	return withWorkspaceStateDB(cwd, func(db *sql.DB) error {
		ctx := context.Background()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		active := nullStringFromPtr(state.ActiveTaskID)
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO workspace_task_meta (id, version, active_task_id, sha256, bytes, updated_at)
			VALUES (1, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				version = excluded.version,
				active_task_id = excluded.active_task_id,
				sha256 = excluded.sha256,
				bytes = excluded.bytes,
				updated_at = excluded.updated_at`,
			state.Version,
			active,
			hex.EncodeToString(sum[:]),
			int64(len(data)),
			storedAt,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_tasks`); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(
			ctx,
			`INSERT INTO workspace_tasks (
				seq, task_id, title, mode, selected_task_proposal_mode, resolved_task_proposal_mode,
				priority, status, reason, acceptance_criteria_json, validation_command, work_branch,
				next_intent_hash, created_at, updated_at, created_by_run, created_by_turn,
				completed_by_run, completed_by_turn, commit_hash, verdict
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i, task := range state.Tasks {
			criteria, err := encodeStringSlice(task.AcceptanceCriteria)
			if err != nil {
				return err
			}
			if _, err := stmt.ExecContext(
				ctx,
				i,
				task.ID,
				task.Title,
				task.Mode,
				task.SelectedTaskProposalMode,
				task.ResolvedTaskProposalMode,
				task.Priority,
				task.Status,
				task.Reason,
				criteria,
				task.ValidationCommand,
				task.WorkBranch,
				task.NextIntentHash,
				task.CreatedAt,
				task.UpdatedAt,
				task.CreatedByRun,
				nullStringFromPtr(task.CreatedByTurn),
				nullStringFromPtr(task.CompletedByRun),
				nullStringFromPtr(task.CompletedByTurn),
				nullStringFromPtr(task.Commit),
				nullStringFromPtr(task.Verdict),
			); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
}

var errWorkspaceStateNotFound = errors.New("workspace state not found")

func withWorkspaceStateDB(cwd string, fn func(*sql.DB) error) error {
	return withWorkspaceStateDBRel(cwd, WorkspaceStateDBPath, true, fn)
}

func withWorkspaceStateDBRel(cwd, dbRel string, create bool, fn func(*sql.DB) error) error {
	if fn == nil {
		return nil
	}
	dbPath, err := workspaceStateDBPath(cwd, dbRel)
	if err != nil {
		return err
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(dbPath), artifact.PrivateDirMode); err != nil {
			return err
		}
		if err := os.Chmod(filepath.Dir(dbPath), artifact.PrivateDirMode); err != nil {
			return err
		}
	} else {
		info, err := os.Lstat(dbPath)
		if errors.Is(err, os.ErrNotExist) {
			return errWorkspaceStateNotFound
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			return security.ErrSymlinkPath
		}
	}
	workspaceStateDBMu.Lock()
	defer workspaceStateDBMu.Unlock()
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(context.Background(), `PRAGMA busy_timeout = 5000`); err != nil {
		return err
	}
	if create {
		if _, err := db.ExecContext(context.Background(), workspaceStateSchema); err != nil {
			return err
		}
	}
	if err := fn(db); err != nil {
		return err
	}
	if !create {
		return nil
	}
	if err := os.Chmod(dbPath, artifact.PrivateFileMode); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func workspaceStateDBPath(cwd, dbRel string) (string, error) {
	return security.SafeJoinNoSymlinks(cwd, dbRel, security.PathPolicy{AllowHidden: true})
}

func sqliteDSN(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func cleanWorkspaceStateRel(rel string) (string, error) {
	return security.CleanRelativePath(rel, security.PathPolicy{AllowHidden: true})
}

func readLegacyWorkspaceJSON(cwd, rel string, target any) (bool, error) {
	path, err := security.SafeJoinNoSymlinks(cwd, rel, security.PathPolicy{AllowHidden: true})
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return false, err
	}
	return true, nil
}

func removeLegacyWorkspaceStateFile(cwd, rel string) error {
	path, err := security.SafeJoinNoSymlinks(cwd, rel, security.PathPolicy{AllowHidden: true})
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func encodeStringSlice(items []string) (string, error) {
	if items == nil {
		items = []string{}
	}
	data, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeStringSlice(raw string, target *[]string) error {
	if raw == "" {
		*target = nil
		return nil
	}
	return json.Unmarshal([]byte(raw), target)
}

func ensureSpecDefaults(state *SpecState) {
	if state.Version == 0 {
		state.Version = 1
	}
}

func ensureTaskDefaults(state *TaskState) {
	if state.Version == 0 {
		state.Version = 1
	}
}

func stringPtrFromNull(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return stringPtr(value.String)
}

func nullStringFromPtr(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func isMissingWorkspaceStateTableError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such table")
}
