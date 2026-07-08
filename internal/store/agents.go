package store

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, base_url, token_hash, enabled, status,
		       host_name, ip_address, version, last_seen_at, last_error,
		       created_at, updated_at
		FROM agents
		ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(
			&a.ID, &a.Code, &a.Name, &a.BaseURL, &a.TokenHash, &a.Enabled, &a.Status,
			&a.HostName, &a.IPAddress, &a.Version, &a.LastSeenAt, &a.LastError,
			&a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, nil
}

func (s *Store) GetAgent(ctx context.Context, id int) (*Agent, error) {
	var a Agent
	err := s.pool.QueryRow(ctx, `
		SELECT id, code, name, base_url, token_hash, enabled, status,
		       host_name, ip_address, version, last_seen_at, last_error,
		       created_at, updated_at
		FROM agents
		WHERE id = $1`, id).
		Scan(
			&a.ID, &a.Code, &a.Name, &a.BaseURL, &a.TokenHash, &a.Enabled, &a.Status,
			&a.HostName, &a.IPAddress, &a.Version, &a.LastSeenAt, &a.LastError,
			&a.CreatedAt, &a.UpdatedAt,
		)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) GetAgentByCode(ctx context.Context, code string) (*Agent, error) {
	var a Agent
	err := s.pool.QueryRow(ctx, `
		SELECT id, code, name, base_url, token_hash, enabled, status,
		       host_name, ip_address, version, last_seen_at, last_error,
		       created_at, updated_at
		FROM agents
		WHERE code = $1`, code).
		Scan(
			&a.ID, &a.Code, &a.Name, &a.BaseURL, &a.TokenHash, &a.Enabled, &a.Status,
			&a.HostName, &a.IPAddress, &a.Version, &a.LastSeenAt, &a.LastError,
			&a.CreatedAt, &a.UpdatedAt,
		)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) CreateAgent(ctx context.Context, a *Agent) (*Agent, error) {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agents
		  (code, name, base_url, token_hash, enabled, status, host_name, ip_address, version, last_error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at, updated_at`,
		a.Code, a.Name, a.BaseURL, a.TokenHash, a.Enabled, a.Status, a.HostName, a.IPAddress, a.Version, a.LastError,
	).Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func (s *Store) UpdateAgent(ctx context.Context, a *Agent) error {
	cmd, err := s.pool.Exec(ctx, `
		UPDATE agents
		SET code=$1, name=$2, base_url=$3, token_hash=$4, enabled=$5, status=$6,
		    host_name=$7, ip_address=$8, version=$9, last_error=$10, updated_at=NOW()
		WHERE id=$11`,
		a.Code, a.Name, a.BaseURL, a.TokenHash, a.Enabled, a.Status,
		a.HostName, a.IPAddress, a.Version, a.LastError, a.ID,
	)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("agent not found")
	}
	return nil
}

func (s *Store) TouchAgentHeartbeat(ctx context.Context, agentID int, hb AgentHeartbeat) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE agents
		SET status='online',
		    host_name=$1,
		    ip_address=$2,
		    version=$3,
		    last_error=$4,
		    last_seen_at=NOW(),
		    updated_at=NOW()
		WHERE id=$5`,
		hb.HostName, hb.IPAddress, hb.Version, hb.LastError, agentID,
	)
	return err
}

func (s *Store) MarkAgentOffline(ctx context.Context, agentID int, lastError string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE agents
		SET status='offline', last_error=$1, updated_at=NOW()
		WHERE id=$2`, lastError, agentID)
	return err
}

func (s *Store) ListNASTargets(ctx context.Context) ([]NASTarget, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, description, mount_type, remote_path, default_subpath, enabled, created_at, updated_at
		FROM nas_targets
		ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []NASTarget
	for rows.Next() {
		var t NASTarget
		if err := rows.Scan(
			&t.ID, &t.Code, &t.Name, &t.Description, &t.MountType, &t.RemotePath, &t.DefaultSubpath, &t.Enabled, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, nil
}

func (s *Store) GetDefaultNASTarget(ctx context.Context) (*NASTarget, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, description, mount_type, remote_path, default_subpath, enabled, created_at, updated_at
		FROM nas_targets
		WHERE enabled = true
		ORDER BY id
		LIMIT 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []NASTarget
	for rows.Next() {
		var t NASTarget
		if err := rows.Scan(
			&t.ID, &t.Code, &t.Name, &t.Description, &t.MountType, &t.RemotePath, &t.DefaultSubpath, &t.Enabled, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	if len(targets) != 1 {
		return nil, nil
	}
	return &targets[0], nil
}

func (s *Store) GetAgentNASMountBase(ctx context.Context, agentID int, nasTargetID *int) (string, error) {
	if nasTargetID == nil {
		return "", nil
	}

	var mountBase string
	err := s.pool.QueryRow(ctx, `
		SELECT mount_base
		FROM agent_nas_targets
		WHERE agent_id = $1
		  AND nas_target_id = $2
		  AND writable = true`,
		agentID, *nasTargetID,
	).Scan(&mountBase)
	if err == nil {
		return mountBase, nil
	}

	var mappingCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_nas_targets
		WHERE agent_id = $1`, agentID).Scan(&mappingCount); err != nil {
		return "", err
	}
	if mappingCount == 0 {
		return "", nil
	}
	return "", err
}

func (s *Store) ResolveProjectNASForAgent(ctx context.Context, project *Project, agentID int) error {
	mountBase, err := s.GetAgentNASMountBase(ctx, agentID, project.NasTargetID)
	if err != nil {
		return err
	}
	if mountBase != "" {
		project.NasBase = mountBase
	}

	if project.NasSubpath == "" && project.NasTargetID != nil {
		target, err := s.GetDefaultNASTarget(ctx)
		if err == nil && target != nil && target.ID == *project.NasTargetID && target.DefaultSubpath != "" {
			project.NasSubpath = filepath.Clean(target.DefaultSubpath)
		}
	}
	return nil
}

func (s *Store) AgentOwnsProject(ctx context.Context, agentID, projectID int) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM projects
			WHERE id = $1
			  AND enabled = true
			  AND executor_type = 'agent'
			  AND executor_agent_id = $2
		)`, projectID, agentID).Scan(&ok)
	return ok, err
}

func (s *Store) AgentOwnsSchedule(ctx context.Context, agentID, scheduleID int) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM schedules s
			JOIN projects p ON p.id = s.project_id
			WHERE s.id = $1
			  AND p.enabled = true
			  AND p.executor_type = 'agent'
			  AND p.executor_agent_id = $2
		)`, scheduleID, agentID).Scan(&ok)
	return ok, err
}

func (s *Store) AgentOwnsRecord(ctx context.Context, agentID int, recordID int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM backup_records r
			JOIN projects p ON p.id = r.project_id
			WHERE r.id = $1
			  AND p.enabled = true
			  AND p.executor_type = 'agent'
			  AND p.executor_agent_id = $2
		)`, recordID, agentID).Scan(&ok)
	return ok, err
}

func (s *Store) AgentSupportsProjectNAS(ctx context.Context, agentID int, nasTargetID *int) (bool, error) {
	if nasTargetID == nil {
		return true, nil
	}

	var mappingCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_nas_targets
		WHERE agent_id = $1`, agentID).Scan(&mappingCount); err != nil {
		return false, err
	}
	if mappingCount == 0 {
		return true, nil
	}

	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM agent_nas_targets
			WHERE agent_id = $1
			  AND nas_target_id = $2
			  AND writable = true
		)`, agentID, *nasTargetID).Scan(&ok)
	return ok, err
}

func (s *Store) IsAgentOnline(lastSeenAt *time.Time) bool {
	if lastSeenAt == nil {
		return false
	}
	return time.Since(*lastSeenAt) <= 2*time.Minute
}
