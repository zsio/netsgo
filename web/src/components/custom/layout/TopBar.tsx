import { useState } from 'react';
import {
  UserPlus, Settings, Network, LogOut, Home
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useAgents } from '@/hooks/use-agents';
import { ConnectionIndicator } from '@/components/custom/common/ConnectionIndicator';
import { AddAgentDialog } from '@/components/custom/agent/AddAgentDialog';
import { useNavigate, useRouterState } from '@tanstack/react-router';
import { api } from '@/lib/api';
import { useAuthStore } from '@/stores/auth-store';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';

export function TopBar() {
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const [showAddAgent, setShowAddAgent] = useState(false);
  const { data: agents } = useAgents();
  const logout = useAuthStore((state) => state.logout);
  const totalAgents = agents?.length ?? 0;
  const onlineAgentCount = agents?.filter((agent) => agent.online).length ?? 0;
  const agentSummary = agents
    ? (totalAgents > 0 ? `${onlineAgentCount}/${totalAgents} 在线 Agent` : '0 Agent')
    : '加载中...';

  const isAdmin = pathname.startsWith('/admin');

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
    <>
      <header className="h-14 flex items-center justify-between px-4 border-b border-border/40 bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60 z-50">
        <div className="flex items-center gap-4">
          {/* Logo Group */}
          <div
            className="flex items-center gap-2.5 cursor-pointer select-none hover:opacity-80 transition-opacity"
            onClick={() => navigate({ to: '/dashboard' })}
          >
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary text-primary-foreground shadow-sm">
              <Network className="h-5 w-5" />
            </div>
            <div className="flex flex-col -space-y-0.5">
              <span className="font-bold text-base tracking-tight leading-tight">NetsGo</span>
              <span className="text-[10px] font-medium text-muted-foreground uppercase tracking-widest leading-tight">Console</span>
            </div>
          </div>

          <div className="w-px h-6 bg-border/60 mx-2" />

          {/* Status Group */}
          <div className="flex items-center gap-3">
            <ConnectionIndicator />
            <div className="flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-md bg-muted/40 border border-border/40 text-muted-foreground">
              {agentSummary}
            </div>
          </div>
        </div>

        <div className="flex items-center gap-2">
          {!isAdmin && (
            <Button variant="secondary" size="sm" onClick={() => setShowAddAgent(true)}>
              <UserPlus className="h-4 w-4 mr-1.5" />
              添加 Agent
            </Button>
          )}

          <div className="w-px h-5 bg-border mx-2" />

          {isAdmin ? (
            <Button variant="ghost" size="icon" className="text-muted-foreground hover:text-foreground" onClick={() => navigate({ to: '/dashboard' })}>
              <Home className="h-5 w-5" />
            </Button>
          ) : (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" className="text-muted-foreground hover:text-foreground">
                  <Settings className="h-5 w-5" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-48">
                <DropdownMenuLabel>系统设置</DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => navigate({ to: '/admin/config' })}>
                  服务配置
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => navigate({ to: '/admin/keys' })}>
                  API Key 管理
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => navigate({ to: '/admin/policies' })}>
                  隧道策略
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => navigate({ to: '/admin/events' })}>
                  审计事件
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => navigate({ to: '/admin/logs' })}>
                  系统日志
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" onClick={handleLogout}>
                  <LogOut className="h-4 w-4 mr-2" />
                  退出登录
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
        </div>
      </header>

      <AddAgentDialog open={showAddAgent} onOpenChange={setShowAddAgent} />
    </>
  );
}
