import { redirect } from '@tanstack/react-router';
import { getStoredAuthState } from '@/stores/auth-store';

export async function requireConsoleAuth() {
  const { isAuthenticated } = getStoredAuthState();
  if (!isAuthenticated) {
    throw redirect({ to: '/login' });
  }

  return { isAuthenticated };
}

export async function redirectFromIndex() {
  const { isAuthenticated } = getStoredAuthState();
  throw redirect({ to: isAuthenticated ? '/dashboard' : '/login' });
}

export async function requireLoginPage() {
  const { isAuthenticated } = getStoredAuthState();
  if (isAuthenticated) {
    throw redirect({ to: '/dashboard' });
  }
}
