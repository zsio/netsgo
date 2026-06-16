import { describe, expect, test } from 'bun:test';

import {
  emptyCredentialForm,
  emptyPasskeyForm,
  emptyPasswordForm,
  emptyUsernameForm,
} from './security-state';

describe('admin security state helpers', () => {
  test('return fresh blank credential objects for clearing sensitive dialog state', () => {
    const first = emptyCredentialForm();
    const second = emptyCredentialForm();
    first.currentPassword = 'secret';
    first.mfaCode = '123456';

    expect(second).toEqual({ currentPassword: '', mfaCode: '' });
  });

  test('preserve non-secret dialog labels while clearing credentials', () => {
    expect(emptyUsernameForm('admin')).toEqual({
      currentPassword: '',
      mfaCode: '',
      newUsername: 'admin',
    });
    expect(emptyPasswordForm()).toEqual({
      currentPassword: '',
      mfaCode: '',
      newPassword: '',
    });
    expect(emptyPasskeyForm('laptop')).toEqual({
      currentPassword: '',
      mfaCode: '',
      name: 'laptop',
    });
  });
});
