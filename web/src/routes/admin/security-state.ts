export type CredentialForm = {
  currentPassword: string;
  mfaCode: string;
};

export type UsernameForm = CredentialForm & { newUsername: string };
export type PasswordForm = CredentialForm & { newPassword: string };
export type PasskeyForm = CredentialForm & { name: string };

export function emptyCredentialForm(): CredentialForm {
  return { currentPassword: '', mfaCode: '' };
}

export function emptyUsernameForm(newUsername = ''): UsernameForm {
  return { ...emptyCredentialForm(), newUsername };
}

export function emptyPasswordForm(): PasswordForm {
  return { ...emptyCredentialForm(), newPassword: '' };
}

export function emptyPasskeyForm(name = ''): PasskeyForm {
  return { ...emptyCredentialForm(), name };
}
