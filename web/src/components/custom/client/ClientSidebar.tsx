import { useState, useMemo } from 'react';
import {
  Search, Server as ServerIcon, Activity, LayoutDashboard,
  Settings, Key, Shield, FileText, Activity as EventIcon,
} from 'lucide-react';
import { Link, useMatch, useRouterState } from '@tanstack/react-router';
import type { Client } from '@/types';
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarFooter,
  SidebarInput,
  SidebarMenu,
  SidebarMenuItem,
  SidebarMenuButton,
  SidebarMenuBadge,
  SidebarRail,
  SidebarSeparator,
} from '@/components/ui/sidebar';

interface ClientSidebarProps {
  clients: Client[];
  isLoading: boolean;
}

const ADMIN_NAV = [
  { path: '/dashboard/admin/config', name: '服务配置', icon: Settings },
  { path: '/dashboard/admin/keys', name: 'Key 管理', icon: Key },
  { path: '/dashboard/admin/policies', name: '隧道策略', icon: Shield },
  { path: '/dashboard/admin/logs', name: '系统日志', icon: FileText },
  { path: '/dashboard/admin/events', name: '审计事件', icon: EventIcon },
];

export function ClientSidebar({ clients, isLoading }: ClientSidebarProps) {
  const [searchQuery, setSearchQuery] = useState('');

  // 从路由匹配获取当前选中的 clientId
  const clientMatch = useMatch({ from: '/dashboard/clients/$clientId', shouldThrow: false });
  const currentClientId = clientMatch?.params?.clientId;

  // 判断当前是否在概览页（无 clientId 且不在 admin 区）
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const isAdmin = pathname.includes('/admin');
  const isOverview = !currentClientId && !isAdmin;

  const filteredClients = useMemo(() => {
    if (!searchQuery.trim()) return clients;
    const q = searchQuery.toLowerCase();
    return clients.filter(
      (a) =>
        a.info.hostname.toLowerCase().includes(q) ||
        a.id.toLowerCase().includes(q) ||
        (a.last_ip || a.info.ip || '').toLowerCase().includes(q),
    );
  }, [clients, searchQuery]);

  // 在线排前面，离线排后面
  const sortedClients = useMemo(() => {
    return [...filteredClients].sort((a, b) => {
      const aOnline = a.online ? 0 : 1;
      const bOnline = b.online ? 0 : 1;
      return aOnline - bOnline;
    });
  }, [filteredClients]);

  return (
    <Sidebar collapsible="offcanvas">
      <SidebarHeader>
        <Link
          to="/dashboard"
          className="flex items-center gap-2.5 px-2 py-2.5 select-none hover:opacity-80 transition-opacity"
        >
          <img src="/logo.svg" alt="NetsGo" className="h-8 w-8" />
          <div className="flex flex-col -space-y-0.5">
            <span className="font-bold text-base tracking-tight leading-tight">NetsGo</span>
            <span className="text-[10px] font-medium text-muted-foreground uppercase tracking-widest leading-tight">Console</span>
          </div>
        </Link>
        <SidebarInput
          placeholder="过滤节点..."
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
        />
      </SidebarHeader>

      <SidebarContent>
        {/* Dashboard 概览入口 */}
        <SidebarGroup>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton asChild isActive={isOverview} tooltip="Dashboard">
                <Link to="/dashboard">
                  <LayoutDashboard />
                  <span>Dashboard</span>
                </Link>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>

        <SidebarSeparator />

        {/* Client 列表 */}
        <SidebarGroup>
          <SidebarGroupContent>
            {isLoading ? (
              <div className="flex flex-col gap-2 px-2 pt-2">
                {[1, 2, 3].map((i) => (
                  <div key={i} className="h-8 w-full rounded-md bg-muted/50 animate-pulse" />
                ))}
              </div>
            ) : clients.length === 0 ? (
              <div className="flex flex-col items-center justify-center text-muted-foreground py-12 px-4 text-center">
                <Activity className="h-10 w-10 mb-3 opacity-20" />
                <p className="text-sm">暂无 Client</p>
                <p className="text-xs opacity-60 mt-1">启动 Client 后将自动显示</p>
              </div>
            ) : filteredClients.length === 0 ? (
              <div className="flex flex-col items-center justify-center text-muted-foreground py-12 px-4 text-center">
                <Search className="h-10 w-10 mb-3 opacity-20" />
                <p className="text-sm">未找到匹配的节点</p>
              </div>
            ) : (
              <SidebarMenu>
                {sortedClients.map((client) => {
                  const isOnline = client.online;
                  const isSelected = currentClientId === client.id;

                  return (
                    <SidebarMenuItem key={client.id}>
                      <SidebarMenuButton
                        asChild
                        isActive={isSelected}
                        tooltip={client.info.hostname}
                        className={!isOnline && !isSelected ? 'opacity-60' : ''}
                      >
                        <Link
                          to="/dashboard/clients/$clientId"
                          params={{ clientId: client.id }}
                        >
                          {isOnline ? (
                            <span className="relative flex h-2 w-2 shrink-0">
                              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                              <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
                            </span>
                          ) : (
                            <span className="h-2 w-2 rounded-full bg-muted-foreground/50 shrink-0" />
                          )}
                          <ServerIcon className="opacity-70 shrink-0" />
                          <span>{client.info.hostname}</span>
                        </Link>
                      </SidebarMenuButton>
                      {(client.proxies?.length ?? 0) > 0 && (
                        <SidebarMenuBadge>
                          {client.proxies!.length}
                        </SidebarMenuBadge>
                      )}
                    </SidebarMenuItem>
                  );
                })}
              </SidebarMenu>
            )}
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>

      {/* 底部 — 系统设置 */}
      <SidebarFooter>
        <SidebarSeparator />
        <SidebarGroup>
          <SidebarGroupLabel>系统设置</SidebarGroupLabel>
          <SidebarMenu>
            {ADMIN_NAV.map((item) => (
              <SidebarMenuItem key={item.path}>
                <SidebarMenuButton
                  asChild
                  isActive={pathname === item.path}
                  tooltip={item.name}
                >
                  <Link to={item.path}>
                    <item.icon />
                    <span>{item.name}</span>
                  </Link>
                </SidebarMenuButton>
              </SidebarMenuItem>
            ))}
          </SidebarMenu>
        </SidebarGroup>
      </SidebarFooter>

      <SidebarRail />
    </Sidebar>
  );
}
