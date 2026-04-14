import { useState, useRef } from 'react';
import { motion, AnimatePresence } from 'motion/react';
import {
  UserPlus, Monitor, Zap, MonitorOff, Pause
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';

import { AddClientDialog } from '@/components/custom/client/AddClientDialog';
import { useNavigate } from '@tanstack/react-router';
import { EMPTY_CONSOLE_SUMMARY } from '@/lib/console-summary';
import { useConsoleSummary } from '@/hooks/use-console-summary';

import { SidebarTrigger, useSidebar } from '@/components/ui/sidebar';
import { Separator } from '@/components/ui/separator';

export function DualTriggerCard({ triggers, children }: { triggers: React.ReactNode, children: React.ReactNode }) {
  const [isOpen, setIsOpen] = useState(false);
  const [isPinned, setIsPinned] = useState(false);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const onMouseEnter = () => {
    clearTimeout(timeoutRef.current);
    setIsOpen(true);
  };

  const onMouseLeave = () => {
    timeoutRef.current = setTimeout(() => {
      setIsOpen(false);
    }, 150);
  };

  const onClick = () => {
    setIsPinned(!isPinned);
    if (!isPinned) {
      setIsOpen(true);
    }
  };

  const onOpenChange = (open: boolean) => {
    if (!open) {
      setIsOpen(false);
      setIsPinned(false);
    } else {
      setIsOpen(true);
    }
  };

  return (
    <Popover open={isOpen || isPinned} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild>
        <div
          onClick={onClick}
          onMouseEnter={onMouseEnter}
          onMouseLeave={onMouseLeave}
        >
          {triggers}
        </div>
      </PopoverTrigger>
      <PopoverContent
        align="center"
        className="w-auto p-3 outline-none"
        onMouseEnter={onMouseEnter}
        onMouseLeave={onMouseLeave}
      >
        {children}
      </PopoverContent>
    </Popover>
  );
}

function TopBarInner() {
  const navigate = useNavigate();
  const [showAddClient, setShowAddClient] = useState(false);
  const { data: summary = EMPTY_CONSOLE_SUMMARY } = useConsoleSummary();

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
    <>
      <header className="h-14 flex items-center justify-between px-4 border-b border-border/40 bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60 z-50 shrink-0 relative">
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
                <div className="flex items-center gap-1.5 px-2 py-1 text-xs font-medium rounded-md bg-muted/40 border border-border/40 text-muted-foreground cursor-pointer hover:bg-muted/80 hover:text-foreground transition-colors group">
                  <Monitor className="h-3.5 w-3.5" />
                  <span className="font-mono tracking-tight">{onlineClientCount}/{totalClients}</span>
                </div>
              }
            >
              <div className="flex flex-col gap-2.5">
                <div className="flex items-center justify-between gap-6 text-sm">
                  <div className="flex items-center gap-2.5 text-emerald-500">
                    <Monitor className="h-4 w-4" />
                    <span className="font-medium">在线节点</span>
                  </div>
                  <span className="font-bold font-mono">{onlineClientCount}</span>
                </div>
                <div className="flex items-center justify-between gap-6 text-sm">
                  <div className="flex items-center gap-2.5 text-rose-500">
                    <MonitorOff className="h-4 w-4" />
                    <span className="font-medium">离线节点</span>
                  </div>
                  <span className="font-bold font-mono">{offlineClientCount}</span>
                </div>
              </div>
            </DualTriggerCard>

            <DualTriggerCard
              triggers={
                <div className="flex items-center gap-1.5 px-2 py-1 text-xs font-medium rounded-md bg-muted/40 border border-border/40 text-muted-foreground cursor-pointer hover:bg-muted/80 hover:text-foreground transition-colors group">
                  <Zap className="h-3.5 w-3.5" />
                  <span className="font-mono tracking-tight">{activeTunnels}/{totalTunnels}</span>
                </div>
              }
            >
                <div className="flex flex-col gap-2.5">
                  <div className="flex items-center justify-between gap-6 text-sm">
                    <div className="flex items-center gap-2.5 text-blue-500">
                      <Zap className="h-4 w-4" />
                      <span className="font-medium">活跃隧道</span>
                  </div>
                  <span className="font-bold font-mono">{activeTunnels}</span>
                </div>
                  <div className="flex items-center justify-between gap-6 text-sm">
                    <div className="flex items-center gap-2.5 text-amber-500">
                      <Pause className="h-4 w-4" />
                      <span className="font-medium">非活跃隧道</span>
                    </div>
                    <span className="font-bold font-mono">{inactiveTunnels}</span>
                  </div>
                </div>
              </DualTriggerCard>
          </motion.div>
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

        <div className="flex items-center gap-2">
          <Button variant="secondary" size="sm" onClick={() => setShowAddClient(true)}>
            <UserPlus className="h-4 w-4 mr-1.5" />
            <span className="hidden sm:inline">添加 Client</span>
          </Button>
        </div>
      </header>

      <AddClientDialog open={showAddClient} onOpenChange={setShowAddClient} />
    </>
  );
}

export function TopBar() {
  return <TopBarInner />;
}
