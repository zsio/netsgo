import { useTranslation } from 'react-i18next';
import {
  LocateFixed,
  Maximize2,
  Minimize2,
  Minus,
  Plus,
  Undo2,
} from 'lucide-react';

import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

export function TopologyBackdrop() {
  return (
    <>
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 bg-white dark:hidden"
      />
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 dark:hidden"
        style={{
          background: '#ffffff',
          backgroundImage: `
            radial-gradient(
              circle at top center,
              rgba(56, 193, 182, 0.25),
              transparent 50%
            )
          `,
          filter: 'blur(200px)',
          backgroundRepeat: 'no-repeat',
        }}
      />
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 hidden bg-black dark:block"
        style={{
          background: 'radial-gradient(ellipse 80% 60% at 50% 0%, rgba(120, 180, 255, 0.5), transparent 70%), #000000',
        }}
      />
    </>
  );
}

export function TopologyHud({
  effectiveFocusId,
  isFullscreen,
  onExitFocus,
  onZoomBy,
  onResetView,
  onToggleFullscreen,
}: {
  effectiveFocusId: string | null;
  isFullscreen: boolean;
  onExitFocus: () => void;
  onZoomBy: (factor: number) => void;
  onResetView: () => void;
  onToggleFullscreen: () => void;
}) {
  const { t } = useTranslation();

  return (
    <>
      {/* 返回全览 */}
      <div
        className={cn(
          'absolute left-3 top-3 z-20 transition-all duration-300',
          effectiveFocusId ? 'translate-y-0 opacity-100' : 'pointer-events-none -translate-y-1 opacity-0',
        )}
      >
        <Button
          type="button"
          variant="secondary"
          size="sm"
          className="h-7 gap-1.5 border border-border/40 bg-background/80 px-2.5 text-xs shadow-sm backdrop-blur-sm hover:bg-background"
          onClick={onExitFocus}
        >
          <Undo2 className="h-3.5 w-3.5" />
          {t('dashboard.topologyBack')}
        </Button>
      </div>

      {/* 缩放控件 */}
      <div className="absolute bottom-3 right-3 z-20 flex flex-col overflow-hidden rounded-lg border border-border/50 bg-background/80 shadow-sm backdrop-blur-sm">
        <button
          type="button"
          className="flex size-7 items-center justify-center text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
          aria-label={t('dashboard.topologyZoomIn')}
          onClick={() => onZoomBy(1.3)}
        >
          <Plus className="size-3.5" />
        </button>
        <button
          type="button"
          className="flex size-7 items-center justify-center border-t border-border/40 text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
          aria-label={t('dashboard.topologyZoomOut')}
          onClick={() => onZoomBy(1 / 1.3)}
        >
          <Minus className="size-3.5" />
        </button>
        <button
          type="button"
          className="flex size-7 items-center justify-center border-t border-border/40 text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
          aria-label={t('dashboard.topologyResetView')}
          onClick={onResetView}
        >
          <LocateFixed className="size-3.5" />
        </button>
        <button
          type="button"
          className="flex size-7 items-center justify-center border-t border-border/40 text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
          aria-label={isFullscreen ? t('dashboard.topologyExitFullscreen') : t('dashboard.topologyFullscreen')}
          onClick={onToggleFullscreen}
        >
          {isFullscreen ? <Minimize2 className="size-3.5" /> : <Maximize2 className="size-3.5" />}
        </button>
      </div>

      {/* 图例 */}
      <div className="pointer-events-none absolute bottom-3 left-3 z-20 flex items-center gap-3 rounded-lg border border-border/40 bg-background/75 px-2.5 py-1.5 text-[10px] text-muted-foreground shadow-sm backdrop-blur-sm">
        <span className="flex items-center gap-1.5">
          <span className="size-1.5 rounded-full bg-emerald-500" />
          {t('tunnels.statusExposed')}
        </span>
        <span className="flex items-center gap-1.5">
          <span className="size-1.5 rounded-full bg-amber-500" />
          {t('tunnels.statusOffline')}
        </span>
        <span className="flex items-center gap-1.5">
          <span className="size-1.5 rounded-full bg-destructive" />
          {t('tunnels.statusError')}
        </span>
        <span className="hidden items-center gap-1.5 sm:flex">
          <svg width="16" height="6" aria-hidden>
            <line x1="0" y1="3" x2="16" y2="3" strokeDasharray="2 4" strokeWidth="1.2" className="stroke-muted-foreground/70" />
          </svg>
          {t('dashboard.topologyControlLink')}
        </span>
      </div>
    </>
  );
}
