import { useEffect, useState } from 'react';
import { motion, AnimatePresence } from 'motion/react';
import {
  Monitor, Zap, MonitorOff, Pause, Github, Star, Loader2, WifiOff,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from '@tanstack/react-router';

import { EMPTY_CONSOLE_SUMMARY } from '@/lib/console-summary';
import { useConsoleSummary } from '@/hooks/use-console-summary';
import { useConnectionStore } from '@/stores/connection-store';
import { DualTriggerCard } from '@/components/custom/common/DualTriggerCard';
import { SidebarTrigger, useSidebar } from '@/components/ui/sidebar';
import { Separator } from '@/components/ui/separator';
import type { ResourceScope } from '@/lib/resource-scope';

export { DualTriggerCard };

const GITHUB_REPO_URL = 'https://github.com/zsio/netsgo';
const SSE_STATUS_DISPLAY_DELAY_MS = 2500;

function ConnectionStatusPill() {
  const { t } = useTranslation();
  const status = useConnectionStore((state) => state.status);
  const isHealthy = status === 'connected' || status === 'connecting';
  const [showDegraded, setShowDegraded] = useState(false);

  useEffect(() => {
    if (isHealthy) {
      return;
    }

    const timer = window.setTimeout(() => {
      setShowDegraded(true);
    }, SSE_STATUS_DISPLAY_DELAY_MS);

    return () => {
      window.clearTimeout(timer);
      setShowDegraded(false);
    };
  }, [isHealthy]);

  const visibleStatus = !isHealthy && showDegraded ? status : null;

  return (
    <AnimatePresence initial={false}>
      {visibleStatus ? (
        <motion.div
          key={visibleStatus}
          initial={{ opacity: 0, scale: 0.95 }}
          animate={{ opacity: 1, scale: 1 }}
          exit={{ opacity: 0, scale: 0.95 }}
          transition={{ duration: 0.18 }}
          role="status"
          aria-live="polite"
          title={visibleStatus === 'reconnecting' ? t('common.sseReconnecting') : t('common.sseDisconnected')}
          className={
            visibleStatus === 'reconnecting'
              ? 'inline-flex h-7 items-center gap-1.5 rounded-full border border-status-warning-border bg-status-warning-soft px-2.5 text-xs font-medium text-status-warning-fg'
              : 'inline-flex h-7 items-center gap-1.5 rounded-full border border-status-danger-border bg-status-danger-soft px-2.5 text-xs font-medium text-status-danger-fg'
          }
        >
          {visibleStatus === 'reconnecting' ? (
            <Loader2 className="h-3 w-3 animate-spin" />
          ) : (
            <WifiOff className="h-3 w-3" />
          )}
          <span className="hidden md:inline">
            {visibleStatus === 'reconnecting' ? t('common.sseReconnecting') : t('common.sseDisconnected')}
          </span>
        </motion.div>
      ) : null}
    </AnimatePresence>
  );
}

function GitHubStarLink() {
  const { t } = useTranslation();

  return (
    <a
      href={GITHUB_REPO_URL}
      target="_blank"
      rel="noreferrer"
      className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border/50 bg-muted/30 px-2 text-xs font-medium text-muted-foreground transition-colors hover:border-border hover:bg-muted/70 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
      title={t('common.githubStar')}
      aria-label={t('common.githubStar')}
    >
      <Github className="h-3.5 w-3.5" />
      <span className="hidden sm:inline">Star</span>
      <Star className="h-3.5 w-3.5 text-primary/85" />
    </a>
  );
}

function TopBarInner({ scope }: { scope: ResourceScope | null }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: summary = EMPTY_CONSOLE_SUMMARY } = useConsoleSummary(scope);

  const totalClients = summary.total_clients;
  const onlineClientCount = summary.online_clients;
  const offlineClientCount = summary.offline_clients;
  const activeTunnels = summary.active_tunnels;
  const totalTunnels = summary.total_tunnels;
  const inactiveTunnels = summary.inactive_tunnels;

  const { state: sidebarState, isMobile, openMobile } = useSidebar();
  const sidebarOpen = sidebarState === 'expanded';

  // On mobile: show logo when sidebar sheet is closed
  // On desktop: show logo when sidebar is collapsed
  const showLogoInHeader = isMobile ? !openMobile : !sidebarOpen;
  // On mobile, center the logo; on desktop, keep it next to the trigger
  const showCenteredLogo = isMobile && !openMobile;

  return (
    <header className="relative z-50 flex h-[calc(3.5rem+var(--safe-area-top))] shrink-0 items-center justify-between border-b border-border/40 bg-background/95 px-4 pl-[calc(1rem+var(--safe-area-left))] pr-[calc(1rem+var(--safe-area-right))] pt-safe-top backdrop-blur supports-[backdrop-filter]:bg-background/60">
      <div className="flex items-center gap-2">
        {/* Sidebar trigger */}
        <SidebarTrigger className="text-muted-foreground hover:text-foreground" />

        {/* Logo — desktop collapsed mode: next to sidebar trigger */}
        <AnimatePresence>
          {showLogoInHeader && !showCenteredLogo && (
            <motion.div
              key="header-logo"
              className="flex items-center gap-2.5"
              initial={{ opacity: 0, x: -8 }}
              animate={{ opacity: 1, x: 0 }}
              exit={{ opacity: 0, x: -8 }}
              transition={{ duration: 0.2, ease: 'easeOut' as const }}
            >
              <Separator orientation="vertical" className="h-6" />
              <div
                className="flex items-center gap-2.5 cursor-pointer select-none hover:opacity-80 transition-opacity"
                onClick={() => navigate({ to: '/dashboard' })}
              >
                <img src="/logo.svg" alt="NetsGo" className="h-8 w-8" />
                <div className="flex flex-col -space-y-0.5">
                  <span className="font-bold text-base tracking-tight leading-tight">NetsGo</span>
                  <span className="text-[10px] font-medium text-muted-foreground uppercase tracking-widest leading-tight">Console</span>
                </div>
              </div>
            </motion.div>
          )}
        </AnimatePresence>

        <div className="w-px h-6 bg-border/60 mx-2 hidden sm:block" />

        {/* Status Group */}
        <AnimatePresence>
          {scope ? (
            <motion.div
              key="header-status"
              className="hidden sm:flex items-center gap-2"
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              transition={{ duration: 0.2 }}
            >
              <DualTriggerCard
                triggers={
                  <div className="flex items-center gap-1.5 px-2 py-1 text-xs font-medium rounded-md bg-muted/45 border border-border/60 text-muted-foreground cursor-pointer hover:bg-muted/80 hover:text-foreground transition-colors group">
                    <Monitor className="h-3.5 w-3.5" />
                    <span className="font-mono tracking-tight">{onlineClientCount}/{totalClients}</span>
                  </div>
                }
              >
                <div className="flex flex-col gap-2.5">
                  <div className="flex items-center justify-between gap-6 text-sm">
                    <div className="flex items-center gap-2.5 text-status-online-fg">
                      <Monitor className="h-4 w-4 text-status-online" />
                      <span className="font-medium">{t('dashboard.onlineNodes')}</span>
                    </div>
                    <span className="font-bold font-mono text-foreground">{onlineClientCount}</span>
                  </div>
                  <div className="flex items-center justify-between gap-6 text-sm">
                    <div className="flex items-center gap-2.5 text-muted-foreground">
                      <MonitorOff className="h-4 w-4" />
                      <span className="font-medium">{t('dashboard.offlineNodes')}</span>
                    </div>
                    <span className="font-bold font-mono text-foreground">{offlineClientCount}</span>
                  </div>
                </div>
              </DualTriggerCard>

              <DualTriggerCard
                triggers={
                  <div className="flex items-center gap-1.5 px-2 py-1 text-xs font-medium rounded-md bg-muted/45 border border-border/60 text-muted-foreground cursor-pointer hover:bg-muted/80 hover:text-foreground transition-colors group">
                    <Zap className="h-3.5 w-3.5 text-primary" />
                    <span className="font-mono tracking-tight">{activeTunnels}/{totalTunnels}</span>
                  </div>
                }
              >
                <div className="flex flex-col gap-2.5">
                  <div className="flex items-center justify-between gap-6 text-sm">
                    <div className="flex items-center gap-2.5 text-brand-strong">
                      <Zap className="h-4 w-4 text-primary" />
                      <span className="font-medium">{t('dashboard.activeTunnels')}</span>
                    </div>
                    <span className="font-bold font-mono text-foreground">{activeTunnels}</span>
                  </div>
                  <div className="flex items-center justify-between gap-6 text-sm">
                    <div className="flex items-center gap-2.5 text-muted-foreground">
                      <Pause className="h-4 w-4" />
                      <span className="font-medium">{t('dashboard.inactiveTunnels')}</span>
                    </div>
                    <span className="font-bold font-mono text-foreground">{inactiveTunnels}</span>
                  </div>
                </div>
              </DualTriggerCard>
            </motion.div>
          ) : null}
        </AnimatePresence>
      </div>

      {/* Logo — mobile centered mode: absolutely centered in header */}
      {showCenteredLogo && (
        <div
          className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 flex items-center gap-2 cursor-pointer select-none hover:opacity-80 transition-opacity"
          onClick={() => navigate({ to: '/dashboard' })}
        >
          <img src="/logo.svg" alt="NetsGo" className="h-7 w-7" />
          <div className="flex flex-col -space-y-0.5">
            <span className="font-bold text-sm tracking-tight leading-tight">NetsGo</span>
            <span className="text-[9px] font-medium text-muted-foreground uppercase tracking-widest leading-tight">Console</span>
          </div>
        </div>
      )}

      <div className="flex items-center justify-end gap-2">
        <ConnectionStatusPill />
        <GitHubStarLink />
      </div>
    </header>
  );
}

export function TopBar({ scope }: { scope: ResourceScope | null }) {
  return <TopBarInner scope={scope} />;
}
