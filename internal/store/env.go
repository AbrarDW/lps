package store

import "context"

// EnvVar is one environment variable of an app.
type EnvVar struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// AppEnv returns an app's environment variables in the order they were saved.
func (s *Store) AppEnv(ctx context.Context, appID int64) ([]EnvVar, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, value, secret FROM app_env WHERE app_id = ? ORDER BY pos, name`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EnvVar{}
	for rows.Next() {
		var v EnvVar
		if err := rows.Scan(&v.Name, &v.Value, &v.Secret); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SetAppEnv replaces all of an app's environment variables.
func (s *Store) SetAppEnv(ctx context.Context, appID int64, vars []EnvVar) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_env WHERE app_id = ?`, appID); err != nil {
		return err
	}
	for i, v := range vars {
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_env (app_id, name, value, secret, pos) VALUES (?, ?, ?, ?, ?)`,
			appID, v.Name, v.Value, v.Secret, i); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit()
}
