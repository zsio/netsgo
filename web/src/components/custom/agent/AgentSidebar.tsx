import {
  Search, ChevronDown, ChevronRight,
  Laptop, Server as ServerIcon, Activity,
} from 'lucide-react';
import { useUIStore } from '@/stores/ui-store';
import { Skeleton } from '@/components/ui/skeleton';
import type { Agent } from '@/types';

interface AgentSidebarProps {
  agents: Agent[];
  isLoading: boolean;
}

export function AgentSidebar({ agents, isLoading }: AgentSidebarProps) {
  const selectedAgentId = useUIStore((s) => s.selectedAgentId);
  const setSelectedAgentId = useUIStore((s) => s.setSelectedAgentId);
  const expandedGroups = useUIStore((s) => s.expandedGroups);
  const toggleGroup = useUIStore((s) => s.toggleGroup);

  const onlineAgents = agents.filter(a => a.stats !== null);
  const offlineAgents = agents.filter(a => a.stats === null);

  return (
    <aside className="w-64 flex flex-col border-r border-border/40 bg-muted/10">
      <div className="p-3">
        <div className="relative">
          <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <input
            type="text"
            placeholder="过滤节点..."
            className="w-full h-9 pl-9 pr-3 rounded-md bg-background border border-border/50 text-sm focus:outline-none focus:ring-2 focus:ring-primary/50 transition-shadow"
          />
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-2 pb-4 select-none">
        {isLoading ? (
          <div className="space-y-2 px-2 pt-2">
            {[1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-8 w-full rounded-md" />
            ))}
          </div>
        ) : agents.length === 0 ? (
          <div className="flex flex-col items-center justify-center text-muted-foreground py-12 px-4 text-center">
            <Activity className="h-10 w-10 mb-3 opacity-20" />
            <p className="text-sm">暂无在线 Agent</p>
            <p className="text-xs opacity-60 mt-1">启动 Agent 后将自动显示</p>
          </div>
        ) : (
          <>
            {/* Active Agents Group */}
            {onlineAgents.length > 0 && (
              <div className="mb-2">
                <div
                  className="flex items-center px-2 py-1.5 text-sm font-medium text-muted-foreground hover:text-foreground cursor-pointer transition-colors"
                  onClick={() => toggleGroup('active')}
                >
                  {expandedGroups['active'] ? <ChevronDown className="h-4 w-4 mr-1" /> : <ChevronRight className="h-4 w-4 mr-1" />}
                  <span className="relative flex h-2 w-2 mr-2">
                    <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                    <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
                  </span>
                  活跃 Agent ({onlineAgents.length})
                </div>

                {expandedGroups['active'] && (
                  <div className="ml-4 space-y-0.5 mt-1">
                    {onlineAgents.map(agent => (
                      <div
                        key={agent.id}
                        className={`flex items-center py-1.5 px-3 rounded-md cursor-pointer text-sm transition-colors ${
                          selectedAgentId === agent.id ? 'bg-primary/10 text-primary font-medium' : 'text-muted-foreground hover:bg-muted/50 hover:text-foreground'
                        }`}
                        onClick={() => setSelectedAgentId(agent.id)}
                      >
                        <ServerIcon className="h-4 w-4 mr-2 opacity-70" />
                        <span className="truncate flex-1">{agent.info.hostname}</span>
                        {(agent.proxies?.length ?? 0) > 0 && (
                          <span className="text-[10px] bg-background border border-border/50 px-1.5 rounded text-muted-foreground">
                            {agent.proxies!.length}
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}

            {/* Offline Agents Group */}
            {offlineAgents.length > 0 && (
              <div className="mb-2">
                <div
                  className="flex items-center px-2 py-1.5 text-sm font-medium text-muted-foreground hover:text-foreground cursor-pointer transition-colors"
                  onClick={() => toggleGroup('offline')}
                >
                  {expandedGroups['offline'] ? <ChevronDown className="h-4 w-4 mr-1" /> : <ChevronRight className="h-4 w-4 mr-1" />}
                  <div className="h-2 w-2 rounded-full bg-muted-foreground/50 mr-2" />
                  离线 Agent ({offlineAgents.length})
                </div>

                {expandedGroups['offline'] && (
                  <div className="ml-4 space-y-0.5 mt-1">
                    {offlineAgents.map(agent => (
                      <div
                        key={agent.id}
                        className={`flex items-center py-1.5 px-3 rounded-md cursor-pointer text-sm transition-colors ${
                          selectedAgentId === agent.id ? 'bg-primary/10 text-primary font-medium' : 'text-muted-foreground opacity-60 hover:opacity-100 hover:bg-muted/50'
                        }`}
                        onClick={() => setSelectedAgentId(agent.id)}
                      >
                        <Laptop className="h-4 w-4 mr-2 opacity-50" />
                        <span className="truncate">{agent.info.hostname}</span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </>
        )}
      </div>
    </aside>
  );
}
