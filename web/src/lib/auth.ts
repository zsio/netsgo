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

  const { isAuthenticated } = getStoredAuthState();
  if (!isAuthenticated) {
    throw redirect({ to: '/login' });
  }

  return { isAuthenticated };
}

export async function redirectFromIndex() {
  const setup = await fetchSetupStatus();
  if (!setup.initialized) {
    throw redirect({ to: '/setup' });
  }

  const { isAuthenticated } = getStoredAuthState();
  throw redirect({ to: isAuthenticated ? '/dashboard' : '/login' });
}

export async function requireLoginPage() {
  const setup = await fetchSetupStatus();
  if (!setup.initialized) {
    throw redirect({ to: '/setup' });
  }

  const { isAuthenticated } = getStoredAuthState();
  if (isAuthenticated) {
    throw redirect({ to: '/dashboard' });
  }
}

export async function requireSetupPage() {
  const setup = await fetchSetupStatus();
  if (!setup.initialized) {
    return;
  }

  const { isAuthenticated } = getStoredAuthState();
  throw redirect({ to: isAuthenticated ? '/dashboard' : '/login' });
}
