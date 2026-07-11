package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	AgentCommandTypeTriggerBackup = "trigger_backup"
	AgentCommandTypeRestoreBackup = "restore_backup"
	AgentCommandTypeReloadSchedule = "reload_schedule"
	AgentCommandTypeRemoveSchedule = "remove_schedule"

	AgentCommandStatusPending = "pending"
	AgentCommandStatusRunning = "running"
	AgentCommandStatusSuccess = "success"
	AgentCommandStatusFailed  = "failed"
)

type AgentCommand struct {
	ID              int64           `json:"id"`
	AgentID         int             `json:"agent_id"`
	AgentCode       string          `json:"agent_code,omitempty"`
	AgentName       string          `json:"agent_name,omitempty"`
	ProjectID       *int            `json:"project_id,omitempty"`
	ProjectName     string          `json:"project_name,omitempty"`
	ScheduleID      *int            `json:"schedule_id,omitempty"`
	RestoreRecordID *int64          `json:"restore_record_id,omitempty"`
	Type            string          `json:"type"`
	Payload         json.RawMessage `json:"payload"`
	Status          string          `json:"status"`
	Result          json.RawMessage `json:"result,omitempty"`
	LogOutput       string          `json:"log_output,omitempty"`
	LogRef          string          `json:"log_ref,omitempty"`
	ErrorMsg        string          `json:"error_msg,omitempty"`
	AttemptCount    int             `json:"attempt_count"`
	CreatedAt       time.Time       `json:"created_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
}

type ListAgentCommandsFilter struct {
	AgentID *int
	Status  string
	Type    string
	Limit   int
	Offset  int
}

func (s *Store) CreateAgentCommand(ctx context.Context, cmd *AgentCommand) (*AgentCommand, error) {
	if len(cmd.Payload) == 0 {
		cmd.Payload = json.RawMessage(`{}`)
	}
	if cmd.Status == "" {
		cmd.Status = AgentCommandStatusPending
	}
	if len(cmd.Result) == 0 {
		cmd.Result = json.RawMessage(`{}`)
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_commands
		  (agent_id, project_id, schedule_id, restore_record_id, type, payload, status, result, log_output, log_ref, error_msg, attempt_count)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, created_at`,
		cmd.AgentID, cmd.ProjectID, cmd.ScheduleID, cmd.RestoreRecordID, cmd.Type, cmd.Payload,
		cmd.Status, cmd.Result, cmd.LogOutput, cmd.LogRef, cmd.ErrorMsg, cmd.AttemptCount,
	).Scan(&cmd.ID, &cmd.CreatedAt)
	return cmd, err
}

func (s *Store) ClaimPendingAgentCommands(ctx context.Context, agentID, limit int) ([]AgentCommand, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		WITH picked AS (
			SELECT id
			FROM agent_commands
			WHERE agent_id = $1
			  AND status = 'pending'
			ORDER BY created_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE agent_commands ac
		SET status = 'running',
		    started_at = NOW(),
		    finished_at = NULL,
		    error_msg = '',
		    result = '{}'::jsonb,
		    attempt_count = attempt_count + 1
		FROM picked
		WHERE ac.id = picked.id
		RETURNING ac.id, ac.agent_id, ac.project_id, ac.schedule_id, ac.restore_record_id,
		          ac.type, ac.payload, ac.status, ac.result, ac.log_output, ac.log_ref,
		          ac.error_msg, ac.attempt_count, ac.created_at, ac.started_at, ac.finished_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commands []AgentCommand
	for rows.Next() {
		var cmd AgentCommand
		if err := rows.Scan(
			&cmd.ID, &cmd.AgentID, &cmd.ProjectID, &cmd.ScheduleID, &cmd.RestoreRecordID,
			&cmd.Type, &cmd.Payload, &cmd.Status, &cmd.Result, &cmd.LogOutput, &cmd.LogRef,
			&cmd.ErrorMsg, &cmd.AttemptCount, &cmd.CreatedAt, &cmd.StartedAt, &cmd.FinishedAt,
		); err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}
	return commands, nil
}

func (s *Store) FinishAgentCommand(ctx context.Context, cmd *AgentCommand) error {
	if cmd == nil || cmd.ID == 0 {
		return fmt.Errorf("agent command missing id")
	}
	if len(cmd.Result) == 0 {
		cmd.Result = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE agent_commands
		SET status=$1, result=$2, log_output=$3, log_ref=$4, error_msg=$5, finished_at=NOW()
		WHERE id=$6`,
		cmd.Status, cmd.Result, cmd.LogOutput, cmd.LogRef, cmd.ErrorMsg, cmd.ID)
	return err
}

func (s *Store) GetAgentCommand(ctx context.Context, id int64) (*AgentCommand, error) {
	var cmd AgentCommand
	err := s.pool.QueryRow(ctx, `
		SELECT ac.id, ac.agent_id, COALESCE(a.code,''), COALESCE(a.name,''),
		       ac.project_id, COALESCE(p.name,''), ac.schedule_id, ac.restore_record_id,
		       ac.type, ac.payload, ac.status, ac.result, ac.log_output, ac.log_ref,
		       ac.error_msg, ac.attempt_count, ac.created_at, ac.started_at, ac.finished_at
		FROM agent_commands ac
		JOIN agents a ON a.id = ac.agent_id
		LEFT JOIN projects p ON p.id = ac.project_id
		WHERE ac.id = $1`, id).
		Scan(&cmd.ID, &cmd.AgentID, &cmd.AgentCode, &cmd.AgentName,
			&cmd.ProjectID, &cmd.ProjectName, &cmd.ScheduleID, &cmd.RestoreRecordID,
			&cmd.Type, &cmd.Payload, &cmd.Status, &cmd.Result, &cmd.LogOutput, &cmd.LogRef,
			&cmd.ErrorMsg, &cmd.AttemptCount, &cmd.CreatedAt, &cmd.StartedAt, &cmd.FinishedAt)
	if err != nil {
		return nil, err
	}
	return &cmd, nil
}

func (s *Store) ListAgentCommands(ctx context.Context, f ListAgentCommandsFilter) ([]AgentCommand, int64, error) {
	where := []string{"1=1"}
	args := []any{}
	i := 1
	if f.AgentID != nil {
		where = append(where, fmt.Sprintf("ac.agent_id = $%d", i))
		args = append(args, *f.AgentID)
		i++
	}
	if status := strings.TrimSpace(f.Status); status != "" {
		where = append(where, fmt.Sprintf("ac.status = $%d", i))
		args = append(args, status)
		i++
	}
	if typ := strings.TrimSpace(f.Type); typ != "" {
		where = append(where, fmt.Sprintf("ac.type = $%d", i))
		args = append(args, typ)
		i++
	}
	clause := strings.Join(where, " AND ")
	if f.Limit <= 0 {
		f.Limit = 50
	}

	var total int64
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM agent_commands ac WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, f.Limit, f.Offset)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT ac.id, ac.agent_id, COALESCE(a.code,''), COALESCE(a.name,''),
		       ac.project_id, COALESCE(p.name,''), ac.schedule_id, ac.restore_record_id,
		       ac.type, ac.payload, ac.status, ac.result, ac.log_output, ac.log_ref,
		       ac.error_msg, ac.attempt_count, ac.created_at, ac.started_at, ac.finished_at
		FROM agent_commands ac
		JOIN agents a ON a.id = ac.agent_id
		LEFT JOIN projects p ON p.id = ac.project_id
		WHERE %s
		ORDER BY ac.created_at DESC, ac.id DESC
		LIMIT $%d OFFSET $%d`, clause, i, i+1), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var commands []AgentCommand
	for rows.Next() {
		var cmd AgentCommand
		if err := rows.Scan(
			&cmd.ID, &cmd.AgentID, &cmd.AgentCode, &cmd.AgentName,
			&cmd.ProjectID, &cmd.ProjectName, &cmd.ScheduleID, &cmd.RestoreRecordID,
			&cmd.Type, &cmd.Payload, &cmd.Status, &cmd.Result, &cmd.LogOutput, &cmd.LogRef,
			&cmd.ErrorMsg, &cmd.AttemptCount, &cmd.CreatedAt, &cmd.StartedAt, &cmd.FinishedAt,
		); err != nil {
			return nil, 0, err
		}
		commands = append(commands, cmd)
	}
	return commands, total, nil
}
