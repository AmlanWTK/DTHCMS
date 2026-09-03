/**
 * `users` — the administration console's people (CP21).
 *
 * The directory, the account page, and the calls behind them. Every write goes through
 * a confirmation with a reason and then a step-up, so nothing here is one click.
 */
export { UserDirectory } from './components/UserDirectory';
export { AccountConsole } from './components/AccountConsole';
export { AdminHome } from './components/AdminHome';
export {
  listUsers,
  getUser,
  listRoles,
  transitionsFor,
  reasonRequiredFor,
  passwordAcceptable,
  permissionsOf,
  EMPLOYEE_CODE,
  type AdminUser,
  type AdminAccount,
  type RoleDefinition,
  type UserStatus,
  type TargetStatus,
} from './api/users';
