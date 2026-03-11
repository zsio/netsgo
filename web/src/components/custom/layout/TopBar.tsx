import {
  Play, Square, Settings, Network,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { ConnectionIndicator } from '@/components/custom/common/ConnectionIndicator';

export function TopBar() {
  return (
    <header className="h-14 flex items-center justify-between px-4 border-b border-border/40 bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60 z-50">
      <div className="flex items-center gap-3">
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/20 text-primary">
          <Network className="h-5 w-5" />
        </div>
        <span className="font-bold text-lg tracking-tight">NetsGo</span>
        <span className="px-2 py-0.5 ml-2 text-xs font-medium rounded-full bg-muted text-muted-foreground border border-border/50">
          Console
        </span>
        <ConnectionIndicator />
      </div>

      <div className="flex items-center gap-2">
        <Button variant="secondary" size="sm">
          <Play className="h-4 w-4 mr-1.5" />
          启动压测
        </Button>
        <Button variant="destructive" size="sm">
          <Square className="h-4 w-4 mr-1.5" />
          停止全隧道
        </Button>
        <div className="w-px h-5 bg-border mx-2" />
        <Button variant="ghost" size="icon" className="text-muted-foreground hover:text-foreground">
          <Settings className="h-5 w-5" />
        </Button>
      </div>
    </header>
  );
}
