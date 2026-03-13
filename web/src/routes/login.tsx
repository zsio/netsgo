import { useState } from 'react';
import { createRoute, useNavigate } from '@tanstack/react-router';
import { rootRoute } from './__root';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useAuthStore } from '@/stores/auth-store';
import { api } from '@/lib/api';
import type { LoginResponse } from '@/types';
import { Server } from 'lucide-react';
import { requireLoginPage } from '@/lib/auth';

export const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  beforeLoad: requireLoginPage,
  component: LoginPage,
});

function LoginPage() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const setAuth = useAuthStore((s) => s.setAuth);
  const navigate = useNavigate();

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);

    try {
      const resp = await api.post<LoginResponse>('/api/auth/login', { username, password });
      setAuth(resp.token, resp.user);
      navigate({ to: '/dashboard' });
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败，请检查用户名和密码');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex h-screen w-full items-center justify-center bg-background absolute inset-0 z-50">
      <div className="w-full max-w-md p-8 rounded-2xl border border-border/50 bg-card flex shadow-lg relative overflow-hidden">
        <div className="absolute top-0 right-0 w-[300px] h-[300px] bg-primary/10 rounded-full blur-3xl pointer-events-none -translate-y-1/2 translate-x-1/2" />
        
        <div className="flex-1 flex w-full flex-col">
          <div className="flex flex-col items-center gap-4 mb-8 relative z-10">
            <div className="p-3 bg-primary/10 rounded-xl">
              <Server className="w-8 h-8 text-primary" />
            </div>
            <h1 className="text-2xl font-bold tracking-tight">NetsGo 管理后台</h1>
            <p className="text-sm text-muted-foreground">请输入管理员账号密码继续</p>
          </div>

          <form onSubmit={handleLogin} className="flex flex-col gap-4 relative z-10 w-full">
            <div className="space-y-2">
              <Input
                type="text"
                placeholder="用户名"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                disabled={loading}
                autoComplete="username"
                required
              />
            </div>
            <div className="space-y-2">
              <Input
                type="password"
                placeholder="密码"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={loading}
                autoComplete="current-password"
                required
              />
            </div>
            
            {error && <div className="text-sm text-destructive mt-1">{error}</div>}
            
            <Button type="submit" className="w-full mt-4" disabled={loading}>
              {loading ? '登录中...' : '登 录'}
            </Button>
          </form>
        </div>
      </div>
    </div>
  );
}
