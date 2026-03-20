import { useMemo } from 'react';
import { motion, useSpring, useTransform } from 'motion/react';
import {
  Server as ServerIcon, Activity, LayoutDashboard,
  Settings, Key,
  Monitor, Zap
} from 'lucide-react';
import { Link, useMatch, useRouterState } from '@tanstack/react-router';
import type { Client } from '@/types';
import { getClientDisplayName } from '@/lib/client-utils';
import { useServerStatus } from '@/hooks/use-server-status';
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarFooter,
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

function AnimatedNumber({ value, className }: { value: number; className?: string }) {
  const spring = useSpring(value, { stiffness: 80, damping: 20 });
  const display = useTransform(spring, (v) => Math.round(v).toString());

  // Update spring target when value changes
  spring.set(value);

  return <motion.span className={className}>{display}</motion.span>;
}

const ADMIN_NAV = [
  { path: '/dashboard/admin/config', name: '服务配置', icon: Settings },
  { path: '/dashboard/admin/keys', name: 'Key 管理', icon: Key },
];

export function ClientSidebar({ clients, isLoading }: ClientSidebarProps) {
  // 从路由匹配获取当前选中的 clientId
  const clientMatch = useMatch({ from: '/dashboard/clients/$clientId', shouldThrow: false });
  const currentClientId = clientMatch?.params?.clientId;

  // 判断当前是否在概览页（路径精确为 /dashboard 且无 clientId）
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const isAdmin = pathname.includes('/admin');
  const isClientPage = pathname.includes('/clients/');
  const isOverview = !currentClientId && !isAdmin && !isClientPage;

  // 在线排前面，离线排后面
  const sortedClients = useMemo(() => {
    return [...clients].sort((a, b) => {
      const aOnline = a.online ? 0 : 1;
      const bOnline = b.online ? 0 : 1;
      return aOnline - bOnline;
    });
  }, [clients]);

  const onlineCount = clients.filter((c) => c.online).length;
  const totalCount = clients.length;

  const { data: status } = useServerStatus();
  const activeTunnels = status?.tunnel_active ?? 0;
  const totalTunnels = activeTunnels + (status?.tunnel_paused ?? 0) + (status?.tunnel_stopped ?? 0);

  return (
    <Sidebar collapsible="offcanvas">
      <SidebarHeader className="p-0">
        <div className="h-14 flex flex-row items-center border-b border-border/40 px-4 shrink-0">
          <Link
            to="/dashboard"
            className="flex items-center gap-2.5 w-full select-none hover:opacity-80 transition-opacity"
          >
            <img src="/logo.svg" alt="NetsGo" className="h-8 w-8" />
            <div className="flex flex-col -space-y-0.5">
              <span className="font-bold text-base tracking-tight leading-tight">NetsGo</span>
              <span className="text-[10px] font-medium text-muted-foreground uppercase tracking-widest leading-tight">Console</span>
            </div>
          </Link>
        </div>
        <div className="flex items-center gap-1.5 px-3 py-3 w-full">
          {/* Node Status Card */}
          <div className="flex-1 flex flex-col bg-muted/40 rounded-lg p-2 border border-border/50 text-xs shadow-sm shadow-black/5">
            <div className="flex items-center justify-between opacity-80 mb-1.5">
              <div className="flex items-center gap-1.5 font-medium text-muted-foreground">
                <Monitor className="h-3 w-3" />
                <span>节点</span>
              </div>
              <div className="relative flex h-1.5 w-1.5">
                {onlineCount > 0 ? (
                  <>
                    <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                    <span className="relative inline-flex rounded-full h-1.5 w-1.5 bg-emerald-500" />
                  </>
                ) : (
                  <span className="relative inline-flex rounded-full h-1.5 w-1.5 bg-muted-foreground/50" />
                )}
              </div>
            </div>
            <div className="flex items-baseline gap-1">
              <span className="text-sm font-bold font-mono tracking-tight">{onlineCount}</span>
              <span className="text-[10px] text-muted-foreground/60 font-mono">/ {totalCount}</span>
            </div>
          </div>

          {/* Tunnel Status Card */}
          <div className="flex-1 flex flex-col bg-muted/40 rounded-lg p-2 border border-border/50 text-xs shadow-sm shadow-black/5">
            <div className="flex items-center justify-between opacity-80 mb-1.5">
              <div className="flex items-center gap-1.5 font-medium text-muted-foreground">
                <Zap className="h-3 w-3" />
                <span>隧道</span>
              </div>
              <div className="relative flex h-1.5 w-1.5">
                {activeTunnels > 0 ? (
                  <>
                    <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-blue-400 opacity-75" />
                    <span className="relative inline-flex rounded-full h-1.5 w-1.5 bg-blue-500" />
                  </>
                ) : (
                  <span className="relative inline-flex rounded-full h-1.5 w-1.5 bg-muted-foreground/50" />
                )}
              </div>
            </div>
            <div className="flex items-baseline gap-1">
              <AnimatedNumber value={activeTunnels} className="text-sm font-bold font-mono tracking-tight" />
              <span className="text-[10px] text-muted-foreground/60 font-mono">/ {totalTunnels}</span>
            </div>
          </div>
        </div>
      </SidebarHeader>

      <SidebarContent>
        {/* Dashboard 概览入口 */}
        <SidebarGroup>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton 
                asChild 
                isActive={isOverview} 
                tooltip="Dashboard"
                className="data-active:bg-primary/15 data-active:text-primary hover:data-active:bg-primary/20"
              >
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
                        tooltip={client.display_name ? `${client.display_name} (${client.info.hostname})` : client.info.hostname}
                        className={`data-active:bg-primary/15 data-active:text-primary hover:data-active:bg-primary/20 ${!isOnline && !isSelected ? 'opacity-60' : ''}`}
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
                          <span>{getClientDisplayName(client)}</span>
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
                  className="data-active:bg-primary/15 data-active:text-primary hover:data-active:bg-primary/20"
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
