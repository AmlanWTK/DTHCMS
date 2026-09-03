package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The identity half of PostgresStore: users, roles and grants (the CP15 Store interface,
// which until CP19 had only an in-memory implementation behind the service).

var _ Store = (*PostgresStore)(nil)

func (s *PostgresStore) GetUser(ctx context.Context, id uuid.UUID) (User, error) {
	return s.UserByID(ctx, id)
}

// EmployeeCode answers the audit module's one question about a person (audit.CodeLookup).
func (s *PostgresStore) EmployeeCode(ctx context.Context, id uuid.UUID) (string, error) {
	u, err := s.UserByID(ctx, id)
	if err != nil {
		return "", err
	}
	return u.Code, nil
}

func (s *PostgresStore) CreateUser(ctx context.Context, u User, by uuid.UUID) (User, error) {
	row, err := s.q.CreateUser(ctx, dbgen.CreateUserParams{
		FacilityID: u.FacilityID, EmployeeCode: u.Code, NameEn: u.NameEN, NameBn: u.NameBN,
		Phone: u.Phone, Email: u.Email, CreatedBy: nullUUID(&by),
	})
	if err != nil {
		return User{}, translate(err)
	}
	return userFromRow(row), nil
}

func (s *PostgresStore) SetUserStatus(ctx context.Context, id uuid.UUID, status Status, reason string, by uuid.UUID) (User, error) {
	row, err := s.q.SetUserStatus(ctx, dbgen.SetUserStatusParams{
		ID: id, Status: string(status), StatusReason: reason, UpdatedBy: nullUUID(&by),
	})
	if err != nil {
		return User{}, translate(err)
	}
	return userFromRow(row), nil
}

func (s *PostgresStore) GetRoleByCode(ctx context.Context, code RoleCode) (Role, error) {
	row, err := s.q.GetRoleByCode(ctx, string(code))
	if err != nil {
		return Role{}, translate(err)
	}
	return roleFromRow(row), nil
}

func roleFromRow(row dbgen.CoreRole) Role {
	r := Role{
		ID: row.ID, Code: RoleCode(row.Code), NameEN: row.NameEn, NameBN: row.NameBn,
		Description: row.Description, IsClinical: row.IsClinical,
	}
	if row.StationCode != nil {
		r.Station = StationCode(*row.StationCode)
	}
	return r
}

// LiveGrants returns the grants not yet revoked, with their role codes.
func (s *PostgresStore) LiveGrants(ctx context.Context, userID uuid.UUID) ([]Grant, error) {
	rows, err := s.q.GrantHistoryForUser(ctx, userID)
	if err != nil {
		return nil, translate(err)
	}
	out := make([]Grant, 0, len(rows))
	for _, row := range rows {
		if row.RevokedAt != nil {
			continue
		}
		out = append(out, Grant{
			ID: row.ID, UserID: row.UserID, RoleID: row.RoleID, RoleCode: RoleCode(row.RoleCode),
			FacilityID: row.FacilityID, GrantedBy: uuidPtr(row.GrantedBy), GrantedAt: row.GrantedAt,
		})
	}
	return out, nil
}

func (s *PostgresStore) GrantRole(ctx context.Context, userID, roleID, facilityID uuid.UUID, by uuid.UUID) (Grant, error) {
	row, err := s.q.GrantRole(ctx, dbgen.GrantRoleParams{
		UserID: userID, RoleID: roleID, FacilityID: facilityID, GrantedBy: nullUUID(&by),
	})
	if err != nil {
		return Grant{}, translate(err)
	}
	return s.grantWithCode(ctx, row)
}

func (s *PostgresStore) RevokeRole(ctx context.Context, userID, roleID uuid.UUID, by uuid.UUID, reason string) (Grant, error) {
	row, err := s.q.RevokeRole(ctx, dbgen.RevokeRoleParams{
		UserID: userID, RoleID: roleID, RevokedBy: nullUUID(&by), RevokeReason: reason,
	})
	if err != nil {
		return Grant{}, translate(err)
	}
	return s.grantWithCode(ctx, row)
}

// grantWithCode fills in the role code a user_role row does not carry.
func (s *PostgresStore) grantWithCode(ctx context.Context, row dbgen.CoreUserRole) (Grant, error) {
	g := Grant{
		ID: row.ID, UserID: row.UserID, RoleID: row.RoleID, FacilityID: row.FacilityID,
		GrantedBy: uuidPtr(row.GrantedBy), GrantedAt: row.GrantedAt,
		RevokedBy: uuidPtr(row.RevokedBy), RevokedAt: row.RevokedAt, RevokeReason: row.RevokeReason,
	}
	role, err := s.q.GetRole(ctx, row.RoleID)
	if err != nil {
		return Grant{}, translate(err)
	}
	g.RoleCode = RoleCode(role.Code)
	return g, nil
}

func (s *PostgresStore) PermissionsForRole(ctx context.Context, code RoleCode) ([]string, error) {
	rows, err := s.q.PermissionsForRole(ctx, string(code))
	if err != nil {
		return nil, translate(err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Code)
	}
	return out, nil
}

// --- the console's reads ---

var _ AdminStore = (*PostgresStore)(nil)

func (s *PostgresStore) ListUsers(ctx context.Context, facilityID uuid.UUID, status *Status) ([]User, error) {
	var filter *string
	if status != nil {
		v := string(*status)
		filter = &v
	}
	rows, err := s.q.ListUsers(ctx, dbgen.ListUsersParams{FacilityID: facilityID, Status: filter})
	if err != nil {
		return nil, translate(err)
	}
	out := make([]User, 0, len(rows))
	for _, row := range rows {
		out = append(out, userFromRow(row))
	}
	return out, nil
}

func (s *PostgresStore) UserByCode(ctx context.Context, facilityID uuid.UUID, code string) (User, error) {
	row, err := s.q.GetUserByEmployeeCode(ctx, dbgen.GetUserByEmployeeCodeParams{FacilityID: facilityID, EmployeeCode: code})
	if err != nil {
		return User{}, translate(err)
	}
	return userFromRow(row), nil
}

// GrantHistory is every grant the person ever had, live and revoked, newest first.
func (s *PostgresStore) GrantHistory(ctx context.Context, userID uuid.UUID) ([]Grant, error) {
	rows, err := s.q.GrantHistoryForUser(ctx, userID)
	if err != nil {
		return nil, translate(err)
	}
	out := make([]Grant, 0, len(rows))
	for _, row := range rows {
		out = append(out, Grant{
			ID: row.ID, UserID: row.UserID, RoleID: row.RoleID, RoleCode: RoleCode(row.RoleCode),
			FacilityID: row.FacilityID, GrantedBy: uuidPtr(row.GrantedBy), GrantedAt: row.GrantedAt,
			RevokedBy: uuidPtr(row.RevokedBy), RevokedAt: row.RevokedAt, RevokeReason: row.RevokeReason,
		})
	}
	return out, nil
}

func (s *PostgresStore) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.q.ListRoles(ctx)
	if err != nil {
		return nil, translate(err)
	}
	out := make([]Role, 0, len(rows))
	for _, row := range rows {
		out = append(out, roleFromRow(row))
	}
	return out, nil
}
