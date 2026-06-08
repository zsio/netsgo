import { describe, expect, test } from 'bun:test';

import { shouldLogoutOnAPIError } from './api';

describe('shouldLogoutOnAPIError', () => {
  test('logs out when the server reports an expired or missing session', () => {
    expect(shouldLogoutOnAPIError(401, 'missing_credentials')).toBe(true);
    expect(shouldLogoutOnAPIError(401, 'session_expired_or_revoked')).toBe(true);
    expect(shouldLogoutOnAPIError(401, undefined)).toBe(true);
  });

  test('keeps the current page for credential verification errors', () => {
    expect(shouldLogoutOnAPIError(401, 'current_password_incorrect')).toBe(false);
    expect(shouldLogoutOnAPIError(401, 'invalid_mfa_code')).toBe(false);
    expect(shouldLogoutOnAPIError(401, 'passkey_login_failed')).toBe(false);
  });

  test('ignores non-auth statuses', () => {
    expect(shouldLogoutOnAPIError(400, 'invalid_request_body')).toBe(false);
    expect(shouldLogoutOnAPIError(500, 'temporary_storage_failure')).toBe(false);
  });
});
