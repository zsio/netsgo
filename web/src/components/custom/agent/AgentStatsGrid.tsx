import {
  Cpu, HardDrive, Globe, ActivitySquare,
  ArrowDownCircle, ArrowUpCircle,
} from 'lucide-react';
import { formatBytes, formatPercent } from '@/lib/format';
import type { Agent } from '@/types';

interface AgentStatsGridProps {
  agent: Agent;
}

export function AgentStatsGrid({ agent }: AgentStatsGridProps) {
  const stats = agent.stats;

  if (!stats) {
    return (
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        {[1, 2, 3, 4].map((i) => (
          <div key={i} className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm h-32 flex items-center justify-center text-muted-foreground text-sm">
            等待数据…
          </div>
        ))}
      </div>
    );
  }

  return (
    <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
      {/* CPU */}
      <div className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm flex flex-col justify-between hover:bg-card/80 transition-colors">
        <div className="flex items-center justify-between text-muted-foreground mb-4">
          <span className="text-sm font-medium">CPU 使用率</span>
          <Cpu className="h-4 w-4" />
        </div>
        <div>
          <div className="text-2xl font-bold">{formatPercent(stats.cpu_usage)}</div>
          <div className="flex items-center gap-2 mt-1">
            <span className="text-xs text-muted-foreground">{stats.num_cpu} 核</span>
          </div>
          <div className="w-full bg-muted rounded-full h-1.5 mt-3 overflow-hidden">
            <div
              className={`h-1.5 rounded-full transition-all duration-500 ${stats.cpu_usage > 80 ? 'bg-destructive' : stats.cpu_usage > 60 ? 'bg-amber-500' : 'bg-emerald-500'}`}
              style={{ width: `${Math.min(stats.cpu_usage, 100)}%` }}
            />
          </div>
        </div>
      </div>

      {/* Memory */}
      <div className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm flex flex-col justify-between hover:bg-card/80 transition-colors">
        <div className="flex items-center justify-between text-muted-foreground mb-4">
          <span className="text-sm font-medium">内存占用</span>
          <ActivitySquare className="h-4 w-4" />
        </div>
        <div>
          <div className="text-2xl font-bold">
            {formatBytes(stats.mem_used)}
            <span className="text-sm font-normal text-muted-foreground"> / {formatBytes(stats.mem_total)}</span>
          </div>
          <div className="w-full bg-muted rounded-full h-1.5 mt-3 overflow-hidden">
            <div
              className={`h-1.5 rounded-full transition-all duration-500 ${stats.mem_usage > 80 ? 'bg-destructive' : stats.mem_usage > 60 ? 'bg-amber-500' : 'bg-emerald-500'}`}
              style={{ width: `${Math.min(stats.mem_usage, 100)}%` }}
            />
          </div>
        </div>
      </div>

      {/* Disk */}
      <div className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm flex flex-col justify-between hover:bg-card/80 transition-colors">
        <div className="flex items-center justify-between text-muted-foreground mb-4">
          <span className="text-sm font-medium">磁盘空间</span>
          <HardDrive className="h-4 w-4" />
        </div>
        <div>
          <div className="text-2xl font-bold">
            {formatPercent(stats.disk_usage)}
          </div>
          <div className="flex items-center gap-2 mt-1">
            <span className="text-xs text-muted-foreground">{formatBytes(stats.disk_used)} / {formatBytes(stats.disk_total)}</span>
          </div>
          <div className="w-full bg-muted rounded-full h-1.5 mt-3 overflow-hidden">
            <div
              className={`h-1.5 rounded-full transition-all duration-500 ${stats.disk_usage > 80 ? 'bg-destructive' : stats.disk_usage > 60 ? 'bg-amber-500' : 'bg-emerald-500'}`}
              style={{ width: `${Math.min(stats.disk_usage, 100)}%` }}
            />
          </div>
        </div>
      </div>

      {/* Network I/O */}
      <div className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm flex flex-col justify-between hover:bg-card/80 transition-colors">
        <div className="flex items-center justify-between text-muted-foreground mb-3">
          <span className="text-sm font-medium">累计网络 I/O</span>
          <Globe className="h-4 w-4" />
        </div>
        <div className="flex flex-col gap-2">
          <div className="flex items-center text-sm">
            <ArrowDownCircle className="h-4 w-4 text-emerald-500 mr-2" />
            <span className="text-muted-foreground w-12">下行</span>
            <span className="font-mono font-medium">{formatBytes(stats.net_recv)}</span>
          </div>
          <div className="flex items-center text-sm">
            <ArrowUpCircle className="h-4 w-4 text-blue-500 mr-2" />
            <span className="text-muted-foreground w-12">上行</span>
            <span className="font-mono font-medium">{formatBytes(stats.net_sent)}</span>
          </div>
        </div>
      </div>
    </div>
  );
}
