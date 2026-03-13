import { redirect } from '@tanstack/react-router';
import type { SetupStatus } from '@/types';
import { getStoredAuthState } from '@/stores/auth-store';

export async function fetchSetupStatus(): Promise<SetupStatus> {
  const response = await fetch('/api/setup/status');
  if (!response.ok) {
    throw new Error(`failed to fetch setup status: ${response.status}`);
  }
  return response.json() as Promise<SetupStatus>;
}

export async function requireConsoleAuth() {
  const setup = await fetchSetupStatus();
  if (!setup.initialized) {
    throw redirect({ to: '/setup' });
  }

  const { token } = getStoredAuthState();
  if (!token) {
    throw redirect({ to: '/login' });
  }

  return { token };
}

export async function redirectFromIndex() {
  const setup = await fetchSetupStatus();
  if (!setup.initialized) {
    throw redirect({ to: '/setup' });
  }

  const { token } = getStoredAuthState();
  throw redirect({ to: token ? '/dashboard' : '/login' });
}

export async function requireLoginPage() {
  const setup = await fetchSetupStatus();
  if (!setup.initialized) {
    throw redirect({ to: '/setup' });
  }

  const { token } = getStoredAuthState();
  if (token) {
    throw redirect({ to: '/dashboard' });
  }
}

export async function requireSetupPage() {
  const setup = await fetchSetupStatus();
  if (!setup.initialized) {
    return;
  }

  const { token } = getStoredAuthState();
  throw redirect({ to: token ? '/dashboard' : '/login' });
}
