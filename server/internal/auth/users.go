package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ListUsers returns every account, by name.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.Query(ctx, "SELECT "+userCols+" FROM users ORDER BY username COLLATE NOCASE")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetUser returns one account, or ErrNotFound.
func (s *Service) GetUser(ctx context.Context, id string) (User, error) {
	u, err := scanUser(s.db.QueryRow(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func isLastAdmin(err error) bool {
	return err != nil && strings.Contains(err.Error(), "last admin")
}

// otherAdmins counts enabled administrators other than id.
func otherAdmins(ctx context.Context, tx *sql.Tx, id string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE role = 'admin' AND disabled = 0 AND id <> ?", id).Scan(&n)
	return n, err
}

func loadUserTx(ctx context.Context, tx *sql.Tx, id string) (User, error) {
	u, err := scanUser(tx.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// NewUser is a request to create an account.
type NewUser struct {
	Username           string
	Password           string
	Role               Role
	MustChangePassword bool
}

// CreateUser adds an account on behalf of an administrator.
func (s *Service) CreateUser(ctx context.Context, a Actor, n NewUser) (User, error) {
	if err := ValidateUsername(n.Username); err != nil {
		return User{}, err
	}
	if !n.Role.Valid() {
		return User{}, &PolicyError{"The role must be admin or user."}
	}
	if err := ValidatePassword(n.Password, n.Username); err != nil {
		return User{}, err
	}
	hash, err := s.h.Hash(ctx, n.Password)
	if err != nil {
		return User{}, err
	}
	now := s.unix()
	u := User{ID: newID(), Username: n.Username, Role: n.Role, MustChangePassword: n.MustChangePassword,
		CreatedAt: time.Unix(now, 0).UTC(), PasswordChangedAt: time.Unix(now, 0).UTC()}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, must_change_password, created_at, password_changed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, u.ID, u.Username, hash, string(u.Role), boolInt(u.MustChangePassword), now, now); err != nil {
			if isUniqueViolation(err) {
				return ErrUsernameTaken
			}
			return err
		}
		return s.auditTx(ctx, tx, a, "user.create", u.Username, map[string]any{"id": u.ID, "role": string(u.Role)})
	})
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// UserPatch changes some fields of an account; nil leaves a field alone.
type UserPatch struct {
	Username *string
	Role     *Role
	Disabled *bool
}

// UpdateUser renames, re-roles, enables or disables an account. The last enabled
// administrator can be neither demoted nor disabled. Disabling ends the
// account's sessions at once; a role change takes effect on the next request,
// because the role is read from the account on every request.
func (s *Service) UpdateUser(ctx context.Context, a Actor, id string, p UserPatch) (User, error) {
	if p.Username == nil && p.Role == nil && p.Disabled == nil {
		return User{}, &PolicyError{"Nothing to change."}
	}
	if p.Username != nil {
		if err := ValidateUsername(*p.Username); err != nil {
			return User{}, err
		}
	}
	if p.Role != nil && !p.Role.Valid() {
		return User{}, &PolicyError{"The role must be admin or user."}
	}
	var out User
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		old, err := loadUserTx(ctx, tx, id)
		if err != nil {
			return err
		}
		next := old
		detail := map[string]any{}
		if p.Username != nil && *p.Username != old.Username {
			next.Username = *p.Username
			detail["username"] = []string{old.Username, next.Username}
		}
		if p.Role != nil && *p.Role != old.Role {
			next.Role = *p.Role
			detail["role"] = []string{string(old.Role), string(next.Role)}
		}
		if p.Disabled != nil && *p.Disabled != old.Disabled {
			next.Disabled = *p.Disabled
			detail["disabled"] = next.Disabled
		}
		// Code-level guard; the trigger in the schema holds even if this is wrong.
		if old.Role == RoleAdmin && !old.Disabled && (next.Role != RoleAdmin || next.Disabled) {
			if n, err := otherAdmins(ctx, tx, id); err != nil {
				return err
			} else if n == 0 {
				return ErrLastAdmin
			}
		}
		if len(detail) > 0 {
			if _, err := tx.ExecContext(ctx, "UPDATE users SET username = ?, role = ?, disabled = ? WHERE id = ?",
				next.Username, string(next.Role), boolInt(next.Disabled), id); err != nil {
				switch {
				case isUniqueViolation(err):
					return ErrUsernameTaken
				case isLastAdmin(err):
					return ErrLastAdmin
				}
				return err
			}
			if next.Disabled && !old.Disabled {
				if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", id); err != nil {
					return err
				}
			}
			action := "user.update"
			if _, ok := detail["role"]; ok {
				action = "user.role"
			}
			if err := s.auditTx(ctx, tx, a, action, old.Username, detail); err != nil {
				return err
			}
		}
		out = next
		return nil
	})
	return out, err
}

// SetPassword is an administrator choosing a new password for an account. The
// account must change it at its next login, and every session it has ends.
func (s *Service) SetPassword(ctx context.Context, a Actor, id, password string) error {
	u, err := s.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if err := ValidatePassword(password, u.Username); err != nil {
		return err
	}
	hash, err := s.h.Hash(ctx, password)
	if err != nil {
		return err
	}
	return s.resetPassword(ctx, a, u, hash, "password.reset")
}

func (s *Service) resetPassword(ctx context.Context, a Actor, u User, hash, action string) error {
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, password_changed_at = ?, must_change_password = 1 WHERE id = ?`,
			hash, s.unix(), u.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", u.ID); err != nil {
			return err
		}
		return s.auditTx(ctx, tx, a, action, u.Username, map[string]any{"id": u.ID})
	})
	if err != nil {
		return err
	}
	s.th.reset(accountKey(u.Username))
	return nil
}

// ResetPasswordCLI is `wheelhouse reset-password`: it sets a generated one-time
// password for the named account, forces a change at the next login, ends its
// sessions and records it as done by "cli". It returns the password, once.
func (s *Service) ResetPasswordCLI(ctx context.Context, username string) (string, error) {
	row, err := s.userByName(ctx, username)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", ErrNotFound
	}
	pw, err := GeneratePassword()
	if err != nil {
		return "", err
	}
	hash, err := s.h.Hash(ctx, pw)
	if err != nil {
		return "", err
	}
	if err := s.resetPassword(ctx, Actor{Name: "cli"}, row.User, hash, "password.reset"); err != nil {
		return "", err
	}
	return pw, nil
}

// DeleteUser removes an account and, with it, its sessions. The last enabled
// administrator cannot be deleted.
func (s *Service) DeleteUser(ctx context.Context, a Actor, id string) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		u, err := loadUserTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if u.Role == RoleAdmin && !u.Disabled {
			if n, err := otherAdmins(ctx, tx, id); err != nil {
				return err
			} else if n == 0 {
				return ErrLastAdmin
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id); err != nil {
			if isLastAdmin(err) {
				return ErrLastAdmin
			}
			return err
		}
		return s.auditTx(ctx, tx, a, "user.delete", u.Username, map[string]any{"id": u.ID, "role": string(u.Role)})
	})
}
