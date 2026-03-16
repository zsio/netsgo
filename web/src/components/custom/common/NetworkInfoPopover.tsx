import { useState, useRef, useCallback } from 'react';
import { Copy, Check, Globe, Wifi, Server, Network } from 'lucide-react';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';

interface IPEntry {
  label: string;
  value: string;
  icon: React.ReactNode;
}

function CopyableIP({ label, value, icon }: IPEntry) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // fallback for older browsers
      const textarea = document.createElement('textarea');
      textarea.value = value;
      textarea.style.position = 'fixed';
      textarea.style.opacity = '0';
      document.body.appendChild(textarea);
      textarea.select();
      document.execCommand('copy');
      document.body.removeChild(textarea);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  };

  return (
    <div className="flex items-center justify-between gap-3 py-1.5 group">
      <div className="flex items-center gap-2 min-w-0">
        <span className="text-muted-foreground shrink-0">{icon}</span>
        <span className="text-xs text-muted-foreground shrink-0 w-16">{label}</span>
        <span className="text-sm font-mono font-medium truncate">{value}</span>
      </div>
      <button
        onClick={handleCopy}
        className="opacity-0 group-hover:opacity-100 transition-opacity text-muted-foreground hover:text-foreground shrink-0 p-0.5 rounded"
        title="复制"
      >
        {copied ? (
          <Check className="h-3.5 w-3.5 text-emerald-500" />
        ) : (
          <Copy className="h-3.5 w-3.5" />
        )}
      </button>
    </div>
  );
}

interface NetworkInfoPopoverProps {
  /** 本地出站 IP */
  localIP?: string;
  /** 公网 IPv4 */
  publicIPv4?: string;
  /** 公网 IPv6 */
  publicIPv6?: string;
  /** 远程连接 IP (服务端观察到的) */
  remoteIP?: string;
  /** 监听端口 (服务端) */
  port?: number;
  /** 触发元素 */
  children: React.ReactNode;
}

export function NetworkInfoPopover({
  localIP,
  publicIPv4,
  publicIPv6,
  remoteIP,
  port,
  children,
}: NetworkInfoPopoverProps) {
  const [open, setOpen] = useState(false);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const cancelClose = useCallback(() => {
    if (closeTimer.current) {
      clearTimeout(closeTimer.current);
      closeTimer.current = null;
    }
  }, []);

  const scheduleClose = useCallback((delay = 150) => {
    cancelClose();
    closeTimer.current = setTimeout(() => setOpen(false), delay);
  }, [cancelClose]);

  const handleEnter = useCallback(() => {
    cancelClose();
    setOpen(true);
  }, [cancelClose]);

  const entries: IPEntry[] = [];

  if (localIP) {
    entries.push({ label: '内网 IP', value: localIP, icon: <Wifi className="h-3.5 w-3.5" /> });
  }
  if (publicIPv4) {
    entries.push({ label: '公网 v4', value: publicIPv4, icon: <Globe className="h-3.5 w-3.5" /> });
  }
  if (publicIPv6) {
    entries.push({ label: '公网 v6', value: publicIPv6, icon: <Network className="h-3.5 w-3.5" /> });
  }
  if (remoteIP && remoteIP !== localIP && remoteIP !== publicIPv4) {
    entries.push({ label: '远程 IP', value: remoteIP, icon: <Server className="h-3.5 w-3.5" /> });
  }

  if (entries.length === 0) {
    return <>{children}</>;
  }

  return (
    <Popover open={open}>
      <PopoverTrigger
        asChild
        onMouseEnter={handleEnter}
        onMouseLeave={() => scheduleClose()}
      >
        {children}
      </PopoverTrigger>
      <PopoverContent
        className="w-auto min-w-[260px] max-w-[380px] p-3"
        side="bottom"
        align="start"
        onMouseEnter={cancelClose}
        onMouseLeave={() => scheduleClose()}
        onOpenAutoFocus={(e) => e.preventDefault()}
        onCloseAutoFocus={(e) => e.preventDefault()}
        onPointerDownOutside={(e) => e.preventDefault()}
        onInteractOutside={(e) => e.preventDefault()}
        onEscapeKeyDown={() => setOpen(false)}
      >
        <div className="flex flex-col">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-2">
            网络信息
          </span>
          <div className="flex flex-col divide-y divide-border/40">
            {entries.map((entry) => (
              <CopyableIP key={entry.label} {...entry} />
            ))}
          </div>
          {port !== undefined && (
            <div className="mt-2 pt-2 border-t border-border/40 flex items-center gap-2 text-xs text-muted-foreground">
              <span>监听端口:</span>
              <span className="font-mono font-medium text-foreground">{port}</span>
            </div>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
