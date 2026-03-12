import { createRoute, Outlet, Link, useNavigate, redirect } from '@tanstack/react-router';
import { rootRoute } from './__root';
import { useAuthStore } from '@/stores/auth-store';
import { Key, Users, FileText, Shield, Activity, LogOut, ArrowLeft } from 'lucide-react';
import { Button } from '@/components/ui/button';

export const adminRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin',
  beforeLoad: ({ location }) => {
    // 检查 auth
    const authString = localStorage.getItem('netsgo-auth');
    if (!authString || !JSON.parse(authString)?.state?.token) {
      throw redirect({
        to: '/login',
        search: { redirect: location.href },
      });
    }
  },
  component: AdminLayout,
});

const NAV_ITEMS = [
  { path: '/admin/keys', name: 'API Key 管理', icon: Key },
  { path: '/admin/accounts', name: '账号管理', icon: Users },
  { path: '/admin/policies', name: '隧道策略', icon: Shield },
  { path: '/admin/logs', name: '系统日志', icon: FileText },
  { path: '/admin/events', name: '审计事件', icon: Activity },
];

function AdminLayout() {
  const logout = useAuthStore((s) => s.logout);
  const user = useAuthStore((s) => s.user);
  const navigate = useNavigate();

  const handleLogout = () => {
    logout();
    navigate({ to: '/login' });
  };

  return (
    <div className="flex flex-col h-full w-full bg-background overflow-hidden relative absolute inset-0 z-40">
      <div className="flex h-14 items-center justify-between px-6 border-b border-border/40 bg-card/50 backdrop-blur-sm z-10 w-full shrink-0">
        <div className="flex items-center gap-4">
          <Button variant="ghost" size="icon" onClick={() => navigate({ to: '/dashboard' })}>
            <ArrowLeft className="h-5 w-5" />
          </Button>
          <div className="font-semibold px-2 py-1 bg-primary/10 text-primary rounded-md">
            系统管理后台
          </div>
        </div>
        <div className="flex items-center gap-4 text-sm text-muted-foreground">
          <span>当前用户: <span className="text-foreground font-medium">{user?.username}</span></span>
          <Button variant="outline" size="sm" onClick={handleLogout} className="gap-2">
            <LogOut className="h-4 w-4" /> 退出
          </Button>
        </div>
      </div>

      <div className="flex flex-1 overflow-hidden z-10 relative">
        <div className="w-56 border-r border-border/40 bg-card/30 flex flex-col p-4 gap-2 shrink-0">
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
        <div className="flex-1 overflow-y-auto bg-background/50 relative p-8">
           <div className="absolute top-0 right-1/4 w-[600px] h-[600px] bg-primary/5 rounded-full blur-3xl pointer-events-none" />
           <Outlet />
        </div>
      </div>
    </div>
  );
}
