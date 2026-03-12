import { useState, useMemo } from 'react';
import {
  Search, Server as ServerIcon, Activity, LayoutDashboard,
} from 'lucide-react';
import { Link, useMatch } from '@tanstack/react-router';
import type { Agent } from '@/types';

interface AgentSidebarProps {
  agents: Agent[];
  isLoading: boolean;
}

export function AgentSidebar({ agents, isLoading }: AgentSidebarProps) {
  const [searchQuery, setSearchQuery] = useState('');

  // 从路由匹配获取当前选中的 agentId
  const agentMatch = useMatch({ from: '/dashboard/agents/$agentId', shouldThrow: false });
  const currentAgentId = agentMatch?.params?.agentId;

  // 判断当前是否在概览页（无 agentId）
  const isOverview = !currentAgentId;

  const filteredAgents = useMemo(() => {
    if (!searchQuery.trim()) return agents;
    const q = searchQuery.toLowerCase();
    return agents.filter(
      (a) =>
        a.info.hostname.toLowerCase().includes(q) ||
        a.id.toLowerCase().includes(q) ||
        a.info.ip.toLowerCase().includes(q),
    );
  }, [agents, searchQuery]);

  // 在线排前面，离线排后面
  const sortedAgents = useMemo(() => {
    return [...filteredAgents].sort((a, b) => {
      const aOnline = a.stats !== null ? 0 : 1;
      const bOnline = b.stats !== null ? 0 : 1;
      return aOnline - bOnline;
    });
  }, [filteredAgents]);

  return (
    <aside className="w-64 flex flex-col border-r border-border/40 bg-muted/10">
      <div className="p-3">
        <div className="relative">
          <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <input
            type="text"
            placeholder="过滤节点..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="w-full h-9 pl-9 pr-3 rounded-md bg-background border border-border/50 text-sm focus:outline-none focus:ring-2 focus:ring-primary/50 transition-shadow"
          />
        </div>
      </div>

      {/* Dashboard 概览入口 */}
      <Link
        to="/dashboard"
        className={`flex items-center mx-3 py-2 px-3 rounded-md cursor-pointer text-sm transition-colors ${
          isOverview
            ? 'bg-primary/10 text-primary font-medium'
            : 'text-muted-foreground hover:bg-muted/50 hover:text-foreground'
        }`}
      >
        <LayoutDashboard className="h-4 w-4 mr-2" />
        Dashboard
      </Link>
      <div className="mx-3 my-2 border-t border-border/30" />

      <div className="flex-1 overflow-y-auto px-2 pb-4 select-none">
        {isLoading ? (
          <div className="space-y-2 px-2 pt-2">
            {[1, 2, 3].map((i) => (
              <div key={i} className="h-8 w-full rounded-md bg-muted/50 animate-pulse" />
            ))}
          </div>
        ) : agents.length === 0 ? (
          <div className="flex flex-col items-center justify-center text-muted-foreground py-12 px-4 text-center">
            <Activity className="h-10 w-10 mb-3 opacity-20" />
            <p className="text-sm">暂无 Agent</p>
            <p className="text-xs opacity-60 mt-1">启动 Agent 后将自动显示</p>
          </div>
        ) : filteredAgents.length === 0 ? (
          <div className="flex flex-col items-center justify-center text-muted-foreground py-12 px-4 text-center">
            <Search className="h-10 w-10 mb-3 opacity-20" />
            <p className="text-sm">未找到匹配的节点</p>
          </div>
        ) : (
          <div className="space-y-0.5">
            {sortedAgents.map((agent) => {
              const isOnline = agent.stats !== null;
              const isSelected = currentAgentId === agent.id;

              return (
                <Link
                  key={agent.id}
                  to="/dashboard/agents/$agentId"
                  params={{ agentId: agent.id }}
                  className={`flex items-center py-1.5 px-3 rounded-md cursor-pointer text-sm transition-colors ${
                    isSelected
                      ? 'bg-primary/10 text-primary font-medium'
                      : isOnline
                        ? 'text-muted-foreground hover:bg-muted/50 hover:text-foreground'
                        : 'text-muted-foreground opacity-60 hover:opacity-100 hover:bg-muted/50'
                  }`}
                >
                  {/* 在线/离线状态点 */}
                  {isOnline ? (
                    <span className="relative flex h-2 w-2 mr-2.5 shrink-0">
                      <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                      <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
                    </span>
                  ) : (
                    <span className="h-2 w-2 rounded-full bg-muted-foreground/50 mr-2.5 shrink-0" />
                  )}

                  <ServerIcon className="h-4 w-4 mr-2 opacity-70 shrink-0" />
                  <span className="truncate flex-1">{agent.info.hostname}</span>

                  {(agent.proxies?.length ?? 0) > 0 && (
                    <span className="text-[10px] bg-background border border-border/50 px-1.5 rounded text-muted-foreground ml-1">
                      {agent.proxies!.length}
                    </span>
                  )}
                </Link>
              );
            })}
          </div>
        )}
      </div>
    </aside>
  );
}
