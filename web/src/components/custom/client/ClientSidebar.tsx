import { useMemo, useState } from 'react';
import {
  Server as ServerIcon, LayoutDashboard,
  Settings, Key,
  LayersPlus, LogOut
} from 'lucide-react';
import { Link, useMatch, useRouterState, useNavigate } from '@tanstack/react-router';
import type { Client } from '@/types';
import { getClientDisplayName } from '@/lib/client-utils';
import { AddClientDialog } from './AddClientDialog';
import { api } from '@/lib/api';
import { useAuthStore } from '@/stores/auth-store';
import { useTranslation } from 'react-i18next';
import { LanguageSwitcher } from '@/components/custom/common/LanguageSwitcher';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog';
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupAction,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarFooter,
  SidebarMenu,
  SidebarMenuItem,
  SidebarMenuButton,
  SidebarMenuBadge,
  SidebarRail,
} from '@/components/ui/sidebar';

interface ClientSidebarProps {
  clients: Client[];
  isLoading: boolean;
}

const ADMIN_NAV = [
  { path: '/dashboard/admin/config', nameKey: 'dashboard.serverConfig', icon: Settings },
  { path: '/dashboard/admin/keys', nameKey: 'dashboard.keyManagement', icon: Key },
];

export function ClientSidebar({ clients, isLoading }: ClientSidebarProps) {
  const { t } = useTranslation();
  const [showAddClient, setShowAddClient] = useState(false);
  const navigate = useNavigate();
  const logout = useAuthStore((state) => state.logout);

  const handleLogout = async () => {
    try {
      await api.post('/api/auth/logout');
    } catch {
      // ignore logout failures and clear local state anyway
    }
    logout();
    navigate({ to: '/login' });
  };

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

  return (
    <Sidebar collapsible="offcanvas">
      <SidebarHeader className="p-0">
        <div className="h-14 flex flex-row items-center px-4 shrink-0 mt-0 mb-0 border-b border-border/40">
          <Link
            to="/dashboard"
            className="flex items-center gap-2.5 w-full select-none hover:opacity-90 transition-opacity"
          >
            <img src="/logo.svg" alt="NetsGo" className="h-8 w-8" />
            <div className="flex flex-col -space-y-0.5">
              <span className="font-bold text-base tracking-tight leading-tight">NetsGo</span>
              <span className="text-[10px] font-medium text-muted-foreground uppercase tracking-widest leading-tight">Console</span>
            </div>
          </Link>
        </div>
      </SidebarHeader>

      <SidebarContent className="gap-0 mt-2">
        {/* 主要入口 - Dashboard */}
        <SidebarGroup>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton 
                asChild 
                isActive={isOverview} 
                tooltip="Dashboard"
                className="data-[active=true]:bg-background data-[active=true]:shadow-sm data-[active=true]:border-l-2 data-[active=true]:border-primary data-[active=true]:text-foreground relative -ml-2 pl-4 rounded-none rounded-r-md font-medium"
              >
                <Link to="/dashboard">
                  <LayoutDashboard className="h-4 w-4" />
                  <span>{t('dashboard.overview')}</span>
                </Link>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>

        {/* 客户端列表 */}
        <SidebarGroup className="group/clients mt-4">
          <SidebarGroupLabel className="text-[11px] font-bold text-muted-foreground/50 uppercase tracking-[0.2em] px-2 mb-1 transition-colors group-hover/clients:text-muted-foreground/70">
            {t('dashboard.clients')}
          </SidebarGroupLabel>
          <SidebarGroupAction 
            onClick={() => setShowAddClient(true)} 
            title={t('dashboard.addClient')}
            className="top-4 cursor-pointer text-muted-foreground hover:text-foreground"
          >
            <LayersPlus />
            <span className="sr-only">{t('dashboard.addClient')}</span>
          </SidebarGroupAction>
          <SidebarGroupContent className='mt-1'>
            {isLoading ? (
              <div className="flex flex-col gap-2 px-2 pt-2">
                {[1, 2, 3].map((i) => (
                  <div key={i} className="h-8 w-full rounded-md bg-muted/50 animate-pulse" />
                ))}
              </div>
            ) : clients.length === 0 ? (
              <div className="px-3 py-6 w-full flex justify-center">
                <button
                  type="button"
                  onClick={() => setShowAddClient(true)}
                  className="group flex flex-col items-center w-full rounded-xl border border-dashed border-border/80 bg-muted/10 transition-colors hover:border-primary/50 hover:bg-muted/40 p-5 focus:outline-none"
                >
                  <div className="h-10 w-10 rounded-full bg-muted/50 flex items-center justify-center mb-3 group-hover:bg-background border border-transparent group-hover:border-border/50 transition-colors">
                    <LayersPlus className="h-5 w-5 text-muted-foreground group-hover:text-primary transition-colors" />
                  </div>
                  
                  <h3 className="text-sm font-medium text-foreground mb-1">
                    {t('dashboard.addNode')}
                  </h3>
                  <p className="text-[11px] text-muted-foreground text-center">
                    {t('dashboard.addNodeDescription')}
                  </p>
                </button>
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
                        className={`data-[active=true]:bg-background data-[active=true]:shadow-[0_1px_2px_rgba(0,0,0,0.05)] data-[active=true]:border-l-[3px] data-[active=true]:border-primary data-[active=true]:text-foreground relative -ml-2 pl-4 rounded-none rounded-r-md font-medium text-muted-foreground hover:text-foreground ${!isOnline && !isSelected ? 'opacity-60' : ''}`}
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
      <SidebarFooter className="pb-[calc(1rem+var(--safe-area-bottom))]">
        <SidebarGroup>
          <SidebarGroupLabel className="text-[11px] font-bold text-muted-foreground/50 uppercase tracking-[0.2em] px-2 mb-1">
            {t('dashboard.systemSettings')}
          </SidebarGroupLabel>
          <SidebarMenu>
            {ADMIN_NAV.map((item) => (
              <SidebarMenuItem key={item.path}>
                <SidebarMenuButton
                  asChild
                  isActive={pathname === item.path}
                  tooltip={t(item.nameKey)}
                  className="data-[active=true]:bg-background data-[active=true]:shadow-[0_1px_2px_rgba(0,0,0,0.05)] data-[active=true]:border-l-[3px] data-[active=true]:border-primary data-[active=true]:text-foreground relative -ml-2 pl-4 rounded-none rounded-r-md font-medium text-muted-foreground hover:text-foreground"
                >
                  <Link to={item.path}>
                    <item.icon />
                    <span>{t(item.nameKey)}</span>
                  </Link>
                </SidebarMenuButton>
              </SidebarMenuItem>
            ))}
          </SidebarMenu>
        </SidebarGroup>
        
        <SidebarGroup className="mt-2 text-muted-foreground/80">
          <div className="px-2 pb-2">
            <LanguageSwitcher />
          </div>
          <SidebarMenu>
            <SidebarMenuItem>
              <AlertDialog>
                <AlertDialogTrigger asChild>
                  <SidebarMenuButton
                    tooltip={t('auth.logout')}
                    className="text-muted-foreground hover:text-destructive hover:bg-destructive/10"
                  >
                    <LogOut className="h-4 w-4" />
                    <span>{t('auth.logout')}</span>
                  </SidebarMenuButton>
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>{t('auth.logoutTitle')}</AlertDialogTitle>
                    <AlertDialogDescription>
                      {t('auth.logoutDescription')}
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
                    <AlertDialogAction onClick={handleLogout}>{t('auth.confirmLogout')}</AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>
      </SidebarFooter>

      <SidebarRail />
      <AddClientDialog open={showAddClient} onOpenChange={setShowAddClient} />
    </Sidebar>
  );
}
