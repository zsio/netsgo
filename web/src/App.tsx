import { useState } from 'react';
import { 
  Search, Play, Square, Settings, Cpu, HardDrive, 
  Activity, Globe, ChevronDown, ChevronRight,
  ArrowDownCircle, ArrowUpCircle, Laptop, Server as ServerIcon, Network,
  ShieldCheck, ActivitySquare
} from 'lucide-react';
import { Button } from '@/components/ui/button';

// --- Dummy Data ---
const agentsData = [
  { id: 'mac-dev', name: 'Mac-Dev', os: 'darwin', status: 'online', cpu: 12.5, mem: '2.4GB / 8GB', disk: '45%', netUp: '1.2 MB/s', netDown: '3.4 MB/s', tunnels: 3 },
  { id: 'ubuntu-prod', name: 'Ubuntu-Prod', os: 'linux', status: 'online', cpu: 45.2, mem: '12GB / 16GB', disk: '80%', netUp: '5.1 MB/s', netDown: '12.4 MB/s', tunnels: 5 },
  { id: 'rpi-home', name: 'RPi-Home', os: 'linux', status: 'offline', cpu: 0, mem: '0 / 0', disk: '0%', netUp: '0 MB/s', netDown: '0 MB/s', tunnels: 0 },
];

const tunnelsData = [
  { id: 't1', name: 'ssh-dev', type: 'TCP', local: '127.0.0.1:22', remote: ':2222', status: 'active', traffic: '1.2M' },
  { id: 't2', name: 'web-test', type: 'HTTP', local: '10.0.0.5:80', remote: ':8080', status: 'active', traffic: '300K' },
  { id: 't3', name: 'db-proxy', type: 'TCP', local: '127.0.0.1:3306', remote: ':33060', status: 'offline', traffic: '0' },
];

export default function App() {
  const [selectedAgentId, setSelectedAgentId] = useState('ubuntu-prod');
  const [expandedGroups, setExpandedGroups] = useState<Record<string, boolean>>({
    'active': true,
    'offline': true,
    'ubuntu-prod': true
  });

  const toggleGroup = (group: string) => {
    setExpandedGroups(prev => ({ ...prev, [group]: !prev[group] }));
  };

  const selectedAgent = agentsData.find(a => a.id === selectedAgentId);

  return (
    <div className="flex flex-col h-screen w-full bg-background text-foreground font-sans selection:bg-primary/30 overflow-hidden">
      
      {/* --- Top Navigation Bar --- */}
      <header className="h-14 flex items-center justify-between px-4 border-b border-border/40 bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60 z-50">
        <div className="flex items-center gap-3">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/20 text-primary">
            <Network className="h-5 w-5" />
          </div>
          <span className="font-bold text-lg tracking-tight">NetsGo</span>
          <span className="px-2 py-0.5 ml-2 text-xs font-medium rounded-full bg-muted text-muted-foreground border border-border/50">
            Console
          </span>
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

      {/* --- Main Workspace --- */}
      <div className="flex flex-1 overflow-hidden">
        
        {/* --- Left Sidebar (Resource Tree) --- */}
        <aside className="w-64 flex flex-col border-r border-border/40 bg-muted/10">
          <div className="p-3">
            <div className="relative">
              <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
              <input 
                type="text" 
                placeholder="过滤节点、隧道..." 
                className="w-full h-9 pl-9 pr-3 rounded-md bg-background border border-border/50 text-sm focus:outline-none focus:ring-2 focus:ring-primary/50 transition-shadow"
              />
            </div>
          </div>

          <div className="flex-1 overflow-y-auto px-2 pb-4 select-none">
            {/* Active Agents Group */}
            <div className="mb-2">
              <div 
                className="flex items-center px-2 py-1.5 text-sm font-medium text-muted-foreground hover:text-foreground cursor-pointer transition-colors"
                onClick={() => toggleGroup('active')}
              >
                {expandedGroups['active'] ? <ChevronDown className="h-4 w-4 mr-1" /> : <ChevronRight className="h-4 w-4 mr-1" />}
                <span className="relative flex h-2 w-2 mr-2">
                  <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
                  <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
                </span>
                活跃 Agent (2)
              </div>
              
              {expandedGroups['active'] && (
                <div className="ml-4 space-y-0.5 mt-1">
                  {agentsData.filter(a => a.status === 'online').map(agent => (
                    <div key={agent.id}>
                      <div 
                        className={`flex items-center py-1.5 px-2 rounded-md cursor-pointer text-sm transition-colors ${
                          selectedAgentId === agent.id ? 'bg-primary/10 text-primary font-medium' : 'text-muted-foreground hover:bg-muted/50 hover:text-foreground'
                        }`}
                        onClick={() => setSelectedAgentId(agent.id)}
                      >
                        {agent.id === 'ubuntu-prod' && (
                           <div onClick={(e) => { e.stopPropagation(); toggleGroup('ubuntu-prod'); }} className="mr-1">
                             {expandedGroups['ubuntu-prod'] ? <ChevronDown className="h-3.5 w-3.5 opacity-50" /> : <ChevronRight className="h-3.5 w-3.5 opacity-50" />}
                           </div>
                        )}
                        {agent.id !== 'ubuntu-prod' && <div className="w-4.5" />}
                        <ServerIcon className="h-4 w-4 mr-2 opacity-70" />
                        <span className="truncate flex-1">{agent.name}</span>
                        <span className="text-[10px] bg-background border border-border/50 px-1.5 rounded text-muted-foreground">{agent.tunnels}</span>
                      </div>
                      
                      {/* Nested Tunnels (Example for ubuntu-prod) */}
                      {agent.id === 'ubuntu-prod' && expandedGroups['ubuntu-prod'] && (
                        <div className="ml-6 mt-1 space-y-1 mb-2 border-l border-border/50 pl-2">
                          <div className="flex items-center py-1 px-2 rounded text-xs text-muted-foreground hover:bg-muted/50 cursor-pointer">
                            <div className="h-1.5 w-1.5 rounded-full bg-emerald-500 mr-2" />
                            <span className="flex-1">ssh-dev</span>
                            <span className="opacity-50 text-[10px] uppercase font-mono">TCP</span>
                          </div>
                          <div className="flex items-center py-1 px-2 rounded text-xs text-muted-foreground hover:bg-muted/50 cursor-pointer">
                            <div className="h-1.5 w-1.5 rounded-full bg-emerald-500 mr-2" />
                            <span className="flex-1">web-test</span>
                            <span className="opacity-50 text-[10px] uppercase font-mono">HTTP</span>
                          </div>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>

            {/* Offline Agents Group */}
            <div className="mb-2">
              <div 
                className="flex items-center px-2 py-1.5 text-sm font-medium text-muted-foreground hover:text-foreground cursor-pointer transition-colors"
                onClick={() => toggleGroup('offline')}
              >
                {expandedGroups['offline'] ? <ChevronDown className="h-4 w-4 mr-1" /> : <ChevronRight className="h-4 w-4 mr-1" />}
                <div className="h-2 w-2 rounded-full bg-muted-foreground/50 mr-2" />
                离线 Agent (1)
              </div>
              
              {expandedGroups['offline'] && (
                <div className="ml-4 space-y-0.5 mt-1">
                  {agentsData.filter(a => a.status === 'offline').map(agent => (
                    <div 
                      key={agent.id}
                      className={`flex items-center py-1.5 px-6 rounded-md cursor-pointer text-sm transition-colors ${
                        selectedAgentId === agent.id ? 'bg-primary/10 text-primary font-medium' : 'text-muted-foreground opacity-60 hover:opacity-100 hover:bg-muted/50'
                      }`}
                      onClick={() => setSelectedAgentId(agent.id)}
                    >
                      <Laptop className="h-4 w-4 mr-2 opacity-50" />
                      <span className="truncate">{agent.name}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>

          </div>
        </aside>

        {/* --- Right Content Area --- */}
        <main className="flex-1 flex flex-col overflow-y-auto bg-background/50 relative">
          
          {/* subtle background glow */}
          <div className="absolute top-0 left-1/4 w-[500px] h-[500px] bg-primary/10 rounded-full blur-3xl pointer-events-none" />

          {selectedAgent ? (
            <div className="p-8 max-w-6xl mx-auto w-full flex flex-col gap-8 z-10">
              
              {/* Header Section */}
              <div className="flex items-start justify-between">
                <div>
                  <div className="flex items-center gap-3 mb-2">
                    <div className="p-2.5 bg-muted rounded-xl border border-border/50">
                      <ServerIcon className="h-6 w-6 text-foreground" />
                    </div>
                    <div>
                      <h1 className="text-2xl font-bold tracking-tight text-foreground flex items-center gap-2">
                        {selectedAgent.name}
                        {selectedAgent.status === 'online' ? (
                          <span className="px-2 py-0.5 rounded text-xs font-medium bg-emerald-500/10 text-emerald-500 border border-emerald-500/20">🟢 在线</span>
                        ) : (
                          <span className="px-2 py-0.5 rounded text-xs font-medium bg-muted text-muted-foreground border border-border">🔴 离线</span>
                        )}
                      </h1>
                      <div className="text-sm text-muted-foreground flex items-center gap-2 mt-1">
                        <span className="font-mono bg-muted/50 px-1.5 py-0.5 rounded">ID: {selectedAgent.id}</span>
                        <span>•</span>
                        <span>{selectedAgent.os === 'darwin' ? 'macOS' : selectedAgent.os === 'linux' ? 'Linux' : 'Windows'}</span>
                        <span>•</span>
                        <span>192.168.1.100</span>
                      </div>
                    </div>
                  </div>
                </div>
                
                <div className="flex gap-2">
                   <Button variant="outline">
                     Web Terminal
                   </Button>
                   <Button variant="default" className="shadow-sm shadow-primary/20">
                     添加隧道
                   </Button>
                </div>
              </div>

              {/* Stats Grid */}
              <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
                <div className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm flex flex-col justify-between hover:bg-card/80 transition-colors">
                  <div className="flex items-center justify-between text-muted-foreground mb-4">
                    <span className="text-sm font-medium">CPU 使用率</span>
                    <Cpu className="h-4 w-4" />
                  </div>
                  <div>
                    <div className="text-2xl font-bold">{selectedAgent.cpu}%</div>
                    <div className="w-full bg-muted rounded-full h-1.5 mt-3 overflow-hidden">
                      <div className={`h-1.5 rounded-full ${selectedAgent.cpu > 80 ? 'bg-destructive' : selectedAgent.cpu > 60 ? 'bg-amber-500' : 'bg-emerald-500'}`} style={{ width: `${selectedAgent.cpu}%` }} />
                    </div>
                  </div>
                </div>

                <div className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm flex flex-col justify-between hover:bg-card/80 transition-colors">
                  <div className="flex items-center justify-between text-muted-foreground mb-4">
                    <span className="text-sm font-medium">内存占用</span>
                    <ActivitySquare className="h-4 w-4" />
                  </div>
                  <div>
                    <div className="text-2xl font-bold">{selectedAgent.mem.split(' / ')[0]} <span className="text-sm font-normal text-muted-foreground">/ {selectedAgent.mem.split(' / ')[1]}</span></div>
                    <div className="w-full bg-muted rounded-full h-1.5 mt-3 overflow-hidden">
                      <div className="bg-amber-500 h-1.5 rounded-full" style={{ width: '45%' }} />
                    </div>
                  </div>
                </div>

                <div className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm flex flex-col justify-between hover:bg-card/80 transition-colors">
                  <div className="flex items-center justify-between text-muted-foreground mb-4">
                    <span className="text-sm font-medium">磁盘空间</span>
                    <HardDrive className="h-4 w-4" />
                  </div>
                  <div>
                    <div className="text-2xl font-bold">{selectedAgent.disk}</div>
                    <div className="w-full bg-muted rounded-full h-1.5 mt-3 overflow-hidden">
                      <div className="bg-emerald-500 h-1.5 rounded-full" style={{ width: selectedAgent.disk }} />
                    </div>
                  </div>
                </div>

                <div className="p-4 rounded-xl border border-border/40 bg-card/50 backdrop-blur-sm shadow-sm flex flex-col justify-between hover:bg-card/80 transition-colors">
                  <div className="flex items-center justify-between text-muted-foreground mb-3">
                    <span className="text-sm font-medium">实时网络 I/O</span>
                    <Globe className="h-4 w-4" />
                  </div>
                  <div className="flex flex-col gap-2">
                    <div className="flex items-center text-sm">
                      <ArrowDownCircle className="h-4 w-4 text-emerald-500 mr-2" />
                      <span className="text-muted-foreground w-12">下行</span>
                      <span className="font-mono font-medium">{selectedAgent.netDown}</span>
                    </div>
                    <div className="flex items-center text-sm">
                      <ArrowUpCircle className="h-4 w-4 text-blue-500 mr-2" />
                      <span className="text-muted-foreground w-12">上行</span>
                      <span className="font-mono font-medium">{selectedAgent.netUp}</span>
                    </div>
                  </div>
                </div>
              </div>

              {/* Tunnels Section */}
              <div className="rounded-xl border border-border/40 bg-card/30 backdrop-blur-sm shadow-sm overflow-hidden">
                <div className="px-6 py-4 border-b border-border/40 flex items-center justify-between bg-card/50">
                  <h3 className="font-semibold text-lg flex items-center gap-2">
                    🚇 下属隧道
                    <span className="bg-muted text-muted-foreground px-2 py-0.5 rounded-full text-xs font-normal">
                      {selectedAgent.tunnels}
                    </span>
                  </h3>
                  <div className="flex gap-2">
                    <div className="relative">
                      <Search className="absolute left-2.5 top-2 h-4 w-4 text-muted-foreground" />
                      <input 
                        type="text" 
                        placeholder="搜索隧道..." 
                        className="h-8 pl-8 pr-3 rounded bg-background border border-border/50 text-xs w-48 focus:outline-none focus:border-primary/50"
                      />
                    </div>
                  </div>
                </div>
                
                {selectedAgent.tunnels > 0 ? (
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm text-left">
                      <thead className="text-xs text-muted-foreground bg-muted/20">
                        <tr>
                          <th className="px-6 py-3 font-medium">名称 / 协议</th>
                          <th className="px-6 py-3 font-medium">本地映射 (Agent端)</th>
                          <th className="px-6 py-3 font-medium">公网入口 (Server端)</th>
                          <th className="px-6 py-3 font-medium">状态</th>
                          <th className="px-6 py-3 font-medium text-right">流量</th>
                          <th className="px-6 py-3 font-medium text-right">操作</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-border/30">
                        {tunnelsData.map((tunnel) => (
                          <tr key={tunnel.id} className="hover:bg-muted/10 transition-colors group">
                            <td className="px-6 py-4">
                              <div className="flex items-center gap-2">
                                <span className="font-medium text-foreground">{tunnel.name}</span>
                                <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-secondary text-secondary-foreground border border-border/50 uppercase">
                                  {tunnel.type}
                                </span>
                              </div>
                            </td>
                            <td className="px-6 py-4 font-mono text-xs text-muted-foreground">{tunnel.local}</td>
                            <td className="px-6 py-4">
                              <span className="font-mono text-xs text-primary bg-primary/10 px-2 py-1 rounded border border-primary/20">
                                {tunnel.remote}
                              </span>
                            </td>
                            <td className="px-6 py-4">
                              {tunnel.status === 'active' ? (
                                <div className="flex items-center text-emerald-500">
                                  <span className="relative flex h-2 w-2 mr-2">
                                    <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
                                    <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
                                  </span>
                                  活跃
                                </div>
                              ) : (
                                <div className="flex items-center text-muted-foreground">
                                  <div className="h-2 w-2 rounded-full bg-muted-foreground/50 mr-2" />
                                  已停止
                                </div>
                              )}
                            </td>
                            <td className="px-6 py-4 text-right font-mono text-xs text-muted-foreground">
                              {tunnel.traffic}
                            </td>
                            <td className="px-6 py-4 text-right">
                              <div className="flex items-center justify-end gap-2 opacity-0 group-hover:opacity-100 transition-opacity">
                                <button className="p-1 hover:bg-secondary rounded text-secondary-foreground" title="设置">
                                  <Settings className="h-4 w-4" />
                                </button>
                                {tunnel.status === 'active' ? (
                                  <button className="p-1 hover:bg-destructive/10 rounded text-destructive" title="停止">
                                    <Square className="h-4 w-4" />
                                  </button>
                                ) : (
                                  <button className="p-1 hover:bg-primary/10 rounded text-primary" title="启动">
                                    <Play className="h-4 w-4" />
                                  </button>
                                )}
                              </div>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
                    <ShieldCheck className="h-12 w-12 mb-4 opacity-20" />
                    <p>该节点暂无随属隧道</p>
                    <Button variant="outline" className="mt-4">
                      + 立即创建
                    </Button>
                  </div>
                )}
              </div>
              
              {/* Network Chart Placeholder */}
              <div className="rounded-xl border border-border/40 bg-card/30 backdrop-blur-sm shadow-sm p-6 relative overflow-hidden group">
                 <div className="flex items-center justify-between mb-6 relative z-10">
                   <h3 className="font-semibold text-lg">📊 流量趋势</h3>
                   <div className="flex gap-2">
                     <span className="text-xs bg-muted px-2 py-1 rounded text-muted-foreground cursor-pointer hover:text-foreground">1h</span>
                     <span className="text-xs bg-muted px-2 py-1 rounded text-muted-foreground cursor-pointer hover:text-foreground">24h</span>
                     <span className="text-xs bg-primary/20 text-primary px-2 py-1 rounded cursor-pointer">7d</span>
                   </div>
                 </div>
                 
                 <div className="h-48 w-full border-b border-l border-border/50 relative z-10 flex items-end">
                   {/* Fake chart lines */}
                   <svg className="w-full h-full text-primary" preserveAspectRatio="none" viewBox="0 0 100 100">
                     <path d="M0,100 L0,50 C20,60 30,20 50,40 C70,60 80,10 100,30 L100,100 Z" fill="currentColor" fillOpacity="0.1" />
                     <path d="M0,50 C20,60 30,20 50,40 C70,60 80,10 100,30" fill="none" stroke="currentColor" strokeWidth="2" strokeOpacity="0.8" />
                   </svg>
                   <svg className="absolute top-0 left-0 w-full h-full text-emerald-500" preserveAspectRatio="none" viewBox="0 0 100 100">
                     <path d="M0,100 L0,80 C20,70 30,90 50,70 C70,50 80,80 100,60 L100,100 Z" fill="currentColor" fillOpacity="0.1" />
                     <path d="M0,80 C20,70 30,90 50,70 C70,50 80,80 100,60" fill="none" stroke="currentColor" strokeWidth="2" strokeOpacity="0.8" />
                   </svg>
                 </div>
                 <div className="absolute inset-0 bg-gradient-to-t from-background/80 to-transparent pointer-events-none" />
              </div>

            </div>
          ) : (
             <div className="flex-1 flex flex-col items-center justify-center text-muted-foreground">
               <Activity className="h-16 w-16 mb-4 opacity-20" />
               <p className="text-lg font-medium">请选择一个节点进行管控</p>
               <p className="text-sm opacity-60 mt-2">支持查看统计指标、配置内网穿透隧道及下发终端指令</p>
             </div>
          )}

        </main>
      </div>
    </div>
  );
}
