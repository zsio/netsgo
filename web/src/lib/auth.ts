import { redirect } from '@tanstack/react-router';
import { getStoredAuthState } from '@/stores/auth-store';

export async function requireConsoleAuth() {
  const { isAuthenticated } = getStoredAuthState();
  if (!isAuthenticated) {
    throw redirect({ to: '/login' });
  }

  return { isAuthenticated };
}

export async function requireActivityAdmin() {
  const { isAuthenticated, user } = getStoredAuthState();
  if (!isAuthenticated) {
    throw redirect({ to: '/login' });
  }
  if (user?.role !== 'admin') {
    throw redirect({ to: '/dashboard' });
  }
  return { isAuthenticated, user };
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
