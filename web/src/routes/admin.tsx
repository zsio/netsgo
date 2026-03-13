import { createRoute, Outlet, Link, useNavigate } from '@tanstack/react-router';
import { rootRoute } from './__root';
import { Key, FileText, Shield, Activity, Settings, LogOut } from 'lucide-react';
import { requireConsoleAuth } from '@/lib/auth';
import { useAuthStore } from '@/stores/auth-store';
import { api } from '@/lib/api';

export const adminRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin',
  beforeLoad: requireConsoleAuth,
  component: AdminLayout,
});

const NAV_ITEMS = [
  { path: '/admin/config', name: '服务配置', icon: Settings },
  { path: '/admin/keys', name: 'API Key 管理', icon: Key },
  { path: '/admin/policies', name: '隧道策略', icon: Shield },
  { path: '/admin/logs', name: '系统日志', icon: FileText },
  { path: '/admin/events', name: '审计事件', icon: Activity },
];

function AdminLayout() {
  const navigate = useNavigate();
  const logout = useAuthStore((s) => s.logout);

  const handleLogout = async () => {
    try {
      await api.post('/api/auth/logout');
    } catch {
      // ignore logout failures and clear local state anyway
    }
    logout();
    navigate({ to: '/login' });
  };

  return (
    <div className="flex-1 w-full bg-background overflow-y-auto">
      <div className="max-w-6xl mx-auto w-full flex p-8 gap-8 items-start">
        {/* 左侧导航栏 */}
        <div className="w-56 shrink-0 flex flex-col gap-2 sticky top-8 self-start">
          <div className="mb-4">
            <h2 className="text-xl font-bold tracking-tight px-1">系统设置</h2>
            <p className="text-xs text-muted-foreground px-1 mt-1">NetsGo 控制台</p>
          </div>
          <div className="flex flex-col gap-1 min-h-[calc(100vh-200px)]">
            <div className="flex flex-col gap-1 flex-1">
              {NAV_ITEMS.map((item) => (
                <Link
                  key={item.path}
                  to={item.path}
                  activeProps={{ className: 'bg-primary/10 text-primary font-medium border-primary/20' }}
                  className="flex items-center gap-3 px-3 py-2.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-colors border border-transparent"
                >
                  <item.icon className="h-4 w-4" />
                  {item.name}
                </Link>
              ))}
            </div>

            <button
              onClick={handleLogout}
              className="flex items-center gap-3 px-3 py-2.5 rounded-md text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors border border-transparent mt-auto"
            >
              <LogOut className="h-4 w-4" />
              退出登录
            </button>
          </div>
        </div>

        {/* 右侧主内容区 */}
        <div className="flex-1 flex flex-col min-w-0 bg-background/50 relative">
          <div className="absolute -top-10 -right-20 w-[600px] h-[600px] bg-primary/5 rounded-full blur-3xl pointer-events-none" />
          <Outlet />
        </div>
      </div>
    </div>
  );
}
