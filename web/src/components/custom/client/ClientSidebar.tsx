import { memo, useMemo, useState } from 'react';
import {
  Server as ServerIcon, LayoutDashboard,
  Settings, Activity, Users, Webhook,
  BookOpen, LayersPlus, Languages, LogOut, Search, ChevronDown, ChevronUp,
  Sun, Moon, LaptopMinimal, ChevronsUpDown, ShieldCheck,
} from 'lucide-react';
import { Link, useRouterState, useNavigate } from '@tanstack/react-router';
import type { Client } from '@/types';
import { cn } from '@/lib/utils';
import { getClientDisplayName } from '@/lib/client-utils';
import { useAddClientDialog } from './add-client-dialog-context';
import { useAuthStore } from '@/stores/auth-store';
import { useThemeStore, type ThemePreference } from '@/stores/theme-store';
import { logoutCurrentSession } from '@/lib/auth';
import type { ResourceScope } from '@/lib/resource-scope';
import { useTranslation } from 'react-i18next';
import { SUPPORTED_LOCALES, type SupportedLocale } from '@/i18n';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
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
  SidebarSeparator,
  useSidebar,
} from '@/components/ui/sidebar';

interface ClientSidebarProps {
  scope: ResourceScope | null;
  clients: Client[];
  isLoading: boolean;
}

const LANGUAGE_LABEL_KEYS: Record<SupportedLocale, string> = {
  'en-US': 'common.english',
  'zh-CN': 'common.chinese',
};

const THEME_LABEL_KEYS: Record<ThemePreference, string> = {
  light: 'common.themeLight',
  dark: 'common.themeDark',
  system: 'common.themeSystem',
};

const DOCS_URL = 'https://netsgo.zs.uy';

const NAV_ITEM_CLASS =
  'rounded-md font-medium text-muted-foreground hover:text-foreground data-[active=true]:bg-brand-soft data-[active=true]:text-brand-strong data-[active=true]:[&_svg]:text-primary';
const SIDEBAR_SEARCH_THRESHOLD = 10;
const SIDEBAR_COLLAPSE_LIMIT = 50;
const SIDEBAR_PING_LIMIT = 20;

const ClientSidebarMenuItem = memo(function ClientSidebarMenuItem({
  clientId,
  displayName,
  tooltip,
  isOnline,
  proxyCount,
  isSelected,
  scopeKind,
  scopeUserId,
  enablePing,
}: {
  clientId: string;
  displayName: string;
  tooltip: string;
  isOnline: boolean;
  proxyCount: number;
  isSelected: boolean;
  scopeKind: ResourceScope['kind'];
  scopeUserId?: string;
  enablePing: boolean;
}) {
  const statusDot = isOnline ? (
    <span className="relative flex h-2 w-2 shrink-0">
      {enablePing && (
        <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-status-online opacity-65" />
      )}
      <span className="relative inline-flex rounded-full h-2 w-2 bg-status-online" />
    </span>
  ) : (
    <span className="h-2 w-2 rounded-full bg-muted-foreground/45 shrink-0" />
  );

  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        asChild
        isActive={isSelected}
        tooltip={tooltip}
        className={cn(NAV_ITEM_CLASS, !isOnline && !isSelected && 'opacity-60')}
      >
        {scopeKind === 'admin-user' && scopeUserId ? (
          <Link
            to="/dashboard/users/$userId/clients/$clientId"
            params={{ userId: scopeUserId, clientId }}
          >
            {statusDot}
            <ServerIcon className="opacity-70 shrink-0" />
            <span>{displayName}</span>
          </Link>
        ) : (
          <Link
            to="/dashboard/clients/$clientId"
            params={{ clientId }}
          >
            {statusDot}
            <ServerIcon className="opacity-70 shrink-0" />
            <span>{displayName}</span>
          </Link>
        )}
      </SidebarMenuButton>
      {proxyCount > 0 && (
        <SidebarMenuBadge>
          {proxyCount}
        </SidebarMenuBadge>
      )}
    </SidebarMenuItem>
  );
});

export function ClientSidebar({ scope, clients, isLoading }: ClientSidebarProps) {
  const { t, i18n } = useTranslation();
  const { openAddClientDialog } = useAddClientDialog();
  const { isMobile } = useSidebar();
  const navigate = useNavigate();
  const principal = useAuthStore((state) => state.user);
  const isAdmin = principal?.is_admin === true;
  const themePreference = useThemeStore((state) => state.preference);
  const cycleTheme = useThemeStore((state) => state.cyclePreference);
  const ThemeIcon = themePreference === 'light' ? Sun : themePreference === 'dark' ? Moon : LaptopMinimal;
  const [searchQuery, setSearchQuery] = useState('');
  const [expanded, setExpanded] = useState(false);
  const [logoutDialogOpen, setLogoutDialogOpen] = useState(false);

  const handleLogout = async () => {
    await logoutCurrentSession();
    navigate({ to: '/login' });
  };

  // 判断当前是否在概览页（路径精确为 /dashboard 且无 clientId）
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const isAdminSection = pathname.includes('/admin');
  const isActivity = pathname.includes('/activity');
  const isWebhooks = pathname.includes('/webhooks');
  const isClientPage = pathname.includes('/clients/');
  const isOverview = !isAdminSection && !isActivity && !isWebhooks && !isClientPage && pathname !== '/dashboard/users';
  const currentLanguage = SUPPORTED_LOCALES.includes(i18n.resolvedLanguage as SupportedLocale)
    ? i18n.resolvedLanguage as SupportedLocale
    : 'en-US';
  const nextLanguage: SupportedLocale = currentLanguage === 'en-US' ? 'zh-CN' : 'en-US';

  // 在线排前面，离线排后面
  const sortedClients = useMemo(() => {
    return [...clients].sort((a, b) => {
      const aOnline = a.online ? 0 : 1;
      const bOnline = b.online ? 0 : 1;
      return aOnline - bOnline;
    });
  }, [clients]);

  const onlineCount = useMemo(
    () => sortedClients.reduce((acc, client) => acc + (client.online ? 1 : 0), 0),
    [sortedClients],
  );

  const filteredClients = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    if (!q) return sortedClients;
    return sortedClients.filter((client) => (
      getClientDisplayName(client).toLowerCase().includes(q)
      || client.info.hostname.toLowerCase().includes(q)
      || (client.info.ip && client.info.ip.toLowerCase().includes(q))
    ));
  }, [searchQuery, sortedClients]);

  const visibleClients = useMemo(() => {
    if (expanded || searchQuery.trim() || filteredClients.length <= SIDEBAR_COLLAPSE_LIMIT) {
      return filteredClients;
    }
    const sliced = filteredClients.slice(0, SIDEBAR_COLLAPSE_LIMIT);
    const selectedBeyondSlice = filteredClients
      .slice(SIDEBAR_COLLAPSE_LIMIT)
      .find((client) => pathname.endsWith(`/clients/${client.id}`));
    return selectedBeyondSlice ? [...sliced, selectedBeyondSlice] : sliced;
  }, [expanded, filteredClients, pathname, searchQuery]);

  const showSearch = sortedClients.length > SIDEBAR_SEARCH_THRESHOLD || searchQuery.length > 0;
  const canToggleExpand = !searchQuery.trim() && filteredClients.length > SIDEBAR_COLLAPSE_LIMIT;

  return (
    <Sidebar collapsible="offcanvas">
      <SidebarHeader className="p-0">
        <div className="h-14 flex flex-row items-center px-4 shrink-0 mt-0 mb-0 border-b border-sidebar-border">
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
                className={NAV_ITEM_CLASS}
              >
                <Link to="/dashboard">
                  <LayoutDashboard className="h-4 w-4" />
                  <span>{t('dashboard.overview')}</span>
                </Link>
              </SidebarMenuButton>
            </SidebarMenuItem>
            {isAdmin ? (
              <SidebarMenuItem>
                <SidebarMenuButton
                  asChild
                  isActive={pathname === '/dashboard/users'}
                  tooltip={t('users.navLabel')}
                  className={NAV_ITEM_CLASS}
                >
                  <Link to="/dashboard/users">
                    <Users className="h-4 w-4" />
                    <span>{t('users.navLabel')}</span>
                  </Link>
                </SidebarMenuButton>
              </SidebarMenuItem>
            ) : null}
            <SidebarMenuItem>
              <SidebarMenuButton
                asChild
                isActive={isActivity}
                tooltip={t('activity.navLabel')}
                className={NAV_ITEM_CLASS}
              >
                <Link to="/dashboard/activity" search={{ scope: 'global', severity: ['info', 'warning', 'error'], category: [] }}>
                  <Activity className="h-4 w-4" />
                  <span>{t('activity.navLabel')}</span>
                </Link>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                asChild
                isActive={isWebhooks}
                tooltip={t('webhooks.navLabel')}
                className={NAV_ITEM_CLASS}
              >
                <Link to="/dashboard/webhooks">
                  <Webhook className="h-4 w-4" />
                  <span>{t('webhooks.navLabel')}</span>
                </Link>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>

        {/* 客户端列表 */}
        {scope ? <SidebarGroup className="group/clients mt-4">
          <SidebarGroupLabel className="text-[11px] font-bold text-muted-foreground/70 uppercase tracking-[0.2em] px-2 mb-1 transition-colors group-hover/clients:text-muted-foreground">
            {t('dashboard.clients')}
          </SidebarGroupLabel>
          <SidebarGroupAction
            onClick={openAddClientDialog}
            title={t('dashboard.addClient')}
            className="top-4 cursor-pointer text-muted-foreground hover:text-foreground"
          >
            <LayersPlus />
            <span className="sr-only">{t('dashboard.addClient')}</span>
          </SidebarGroupAction>
          <SidebarGroupContent className="mt-1">
            {showSearch && !isLoading && clients.length > 0 && (
              <div className="px-2 pb-2">
                <div className="relative">
                  <Search className="pointer-events-none absolute left-2 top-2 h-3.5 w-3.5 text-muted-foreground" />
                  <input
                    type="text"
                    value={searchQuery}
                    onChange={(e) => setSearchQuery(e.target.value)}
                    placeholder={t('dashboard.searchClientsPlaceholder')}
                    className="h-7 w-full rounded-md border border-sidebar-border bg-background/60 pl-7 pr-2 text-xs text-foreground placeholder:text-muted-foreground focus:border-primary/50 focus:outline-none"
                  />
                </div>
              </div>
            )}
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
                  onClick={openAddClientDialog}
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
            ) : visibleClients.length === 0 ? (
              <div className="px-3 py-4 text-center text-xs text-muted-foreground">
                {t('dashboard.noMatchingClients')}
              </div>
            ) : (
              <>
                <SidebarMenu>
                  {visibleClients.map((client) => {
                    const isSelected = pathname.endsWith(`/clients/${client.id}`);
                    const tooltip = client.display_name
                      ? `${client.display_name} (${client.info.hostname})`
                      : client.info.hostname;

                    return (
                      <ClientSidebarMenuItem
                        key={client.id}
                        clientId={client.id}
                        displayName={getClientDisplayName(client)}
                        tooltip={tooltip}
                        isOnline={client.online}
                        proxyCount={client.proxies?.length ?? 0}
                        isSelected={isSelected}
                        scopeKind={scope.kind}
                        scopeUserId={scope.kind === 'admin-user' ? scope.userId : undefined}
                        enablePing={onlineCount <= SIDEBAR_PING_LIMIT || isSelected}
                      />
                    );
                  })}
                </SidebarMenu>
                {canToggleExpand && (
                  <button
                    type="button"
                    onClick={() => setExpanded((prev) => !prev)}
                    className="mt-1 flex w-full items-center justify-center gap-1 rounded-md px-2 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-foreground"
                  >
                    {expanded ? (
                      <>
                        <ChevronUp className="h-3.5 w-3.5" />
                        <span>{t('dashboard.collapseClients')}</span>
                      </>
                    ) : (
                      <>
                        <ChevronDown className="h-3.5 w-3.5" />
                        <span>{t('dashboard.showAllClients', { count: filteredClients.length })}</span>
                      </>
                    )}
                  </button>
                )}
              </>
            )}
          </SidebarGroupContent>
        </SidebarGroup> : null}
      </SidebarContent>

      <SidebarFooter className="pb-[calc(0.75rem+var(--safe-area-bottom))]">
        <SidebarGroup className="p-1 text-muted-foreground/80">
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                asChild
                tooltip={t('common.docs')}
                className={NAV_ITEM_CLASS}
              >
                <a href={DOCS_URL} target="_blank" rel="noreferrer">
                  <BookOpen className="h-4 w-4" />
                  <span>{t('common.docs')}</span>
                </a>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip={t('common.theme')}
                className={NAV_ITEM_CLASS}
                onClick={cycleTheme}
              >
                <ThemeIcon className="h-4 w-4" />
                <span>{t('common.theme')}</span>
                <span className="ml-auto text-xs text-muted-foreground/70">{t(THEME_LABEL_KEYS[themePreference])}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip={t('common.language')}
                className={NAV_ITEM_CLASS}
                onClick={() => void i18n.changeLanguage(nextLanguage)}
              >
                <Languages className="h-4 w-4" />
                <span>{t('common.language')}</span>
                <span className="ml-auto text-xs text-muted-foreground/70">{t(LANGUAGE_LABEL_KEYS[currentLanguage])}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            {isAdmin ? (
              <SidebarMenuItem>
                <SidebarMenuButton
                  asChild
                  isActive={pathname.startsWith('/dashboard/admin')}
                  tooltip={t('dashboard.systemSettings')}
                  className={NAV_ITEM_CLASS}
                >
                  <Link to="/dashboard/admin/config">
                    <Settings className="h-4 w-4" />
                    <span>{t('dashboard.systemSettings')}</span>
                  </Link>
                </SidebarMenuButton>
              </SidebarMenuItem>
            ) : null}
          </SidebarMenu>
        </SidebarGroup>

        {principal ? (
          <>
            <SidebarSeparator className="mx-2 my-0.5" />
            <SidebarMenu className="px-1">
              <SidebarMenuItem>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <SidebarMenuButton
                      size="lg"
                      tooltip={principal.username}
                      className="rounded-lg px-2 data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
                    >
                      <div className="flex size-7 shrink-0 items-center justify-center rounded-md border border-brand-soft-border bg-brand-soft text-xs font-semibold uppercase text-brand-strong">
                        {principal.username.slice(0, 1) || 'U'}
                      </div>
                      <div className="grid flex-1 text-left text-sm leading-tight">
                        <span className="truncate font-semibold text-sidebar-foreground">
                          {principal.username}
                        </span>
                        <span className="truncate text-[11px] text-muted-foreground">
                          {principal.is_admin ? t('users.admin') : t('users.member')}
                        </span>
                      </div>
                      <ChevronsUpDown className="ml-auto size-4 text-muted-foreground" />
                    </SidebarMenuButton>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent
                    className="w-56 rounded-lg"
                    side={isMobile ? 'top' : 'right'}
                    align="end"
                    sideOffset={6}
                  >
                    <DropdownMenuLabel className="p-0 font-normal">
                      <div className="flex items-center gap-2.5 px-2.5 py-2 text-left text-sm">
                        <div className="flex size-7 shrink-0 items-center justify-center rounded-md border border-brand-soft-border bg-brand-soft text-xs font-semibold uppercase text-brand-strong">
                          {principal.username.slice(0, 1) || 'U'}
                        </div>
                        <div className="grid flex-1 text-left text-sm leading-tight">
                          <span className="truncate font-semibold">{principal.username}</span>
                          <span className="truncate text-xs text-muted-foreground">
                            {principal.is_admin ? t('users.admin') : t('users.member')}
                          </span>
                        </div>
                      </div>
                    </DropdownMenuLabel>
                    {isAdmin ? (
                      <>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem asChild>
                          <Link to="/dashboard/admin/security" className="cursor-pointer">
                            <ShieldCheck className="size-4" />
                            <span>{t('admin.accountSecurityShortcut')}</span>
                          </Link>
                        </DropdownMenuItem>
                      </>
                    ) : null}
                    <DropdownMenuSeparator />
                    <DropdownMenuItem
                      variant="destructive"
                      className="cursor-pointer"
                      onSelect={() => setLogoutDialogOpen(true)}
                    >
                      <LogOut className="size-4" />
                      <span>{t('auth.logout')}</span>
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </SidebarMenuItem>
            </SidebarMenu>
          </>
        ) : null}

        <AlertDialog open={logoutDialogOpen} onOpenChange={setLogoutDialogOpen}>
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
      </SidebarFooter>

      <SidebarRail />
    </Sidebar>
  );
}
