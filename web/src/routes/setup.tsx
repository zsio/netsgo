import { useState, useCallback, useRef, useEffect } from 'react';
import { createRoute, useNavigate } from '@tanstack/react-router';
import { rootRoute } from './__root';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useAuthStore } from '@/stores/auth-store';
import { api } from '@/lib/api';
import { fetchSetupStatus } from '@/lib/auth';
import type { SetupResponse, PortRange } from '@/types';
import { Globe, Shield, Check, Copy, Plus, X, Sparkles, ArrowRight, ArrowLeft, AlertTriangle, User, Lock, Loader2, ShieldCheck, Server, KeyRound } from 'lucide-react';
import { requireSetupPage } from '@/lib/auth';
import { useParticleCanvas } from '@/hooks/use-particle-canvas';
import { motion, AnimatePresence } from 'motion/react';

export const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/setup',
  beforeLoad: requireSetupPage,
  component: SetupPage,
});

// 快捷端口预设
const PORT_PRESETS = [
  { label: 'Web 服务', range: { start: 8000, end: 9000 } },
  { label: '开发端口', range: { start: 3000, end: 3999 } },
  { label: '高端口', range: { start: 10000, end: 60000 } },
];

function SetupPage() {
  const [step, setStep] = useState(0);
  const navigate = useNavigate();
  const setAuth = useAuthStore((s) => s.setAuth);
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useParticleCanvas(canvasRef);

  // Step 1: Admin
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [adminError, setAdminError] = useState('');

  // Step 2: Server address
  const [serverAddr, setServerAddr] = useState(() => {
    return window.location.origin;
  });
  const [addrError, setAddrError] = useState('');

  // Step 3: Ports
  const [ports, setPorts] = useState<PortRange[]>([]);
  const [portStart, setPortStart] = useState('');
  const [portEnd, setPortEnd] = useState('');
  const [portError, setPortError] = useState('');

  // Success
  const [loading, setLoading] = useState(false);
  const [submitError, setSubmitError] = useState('');
  const [result, setResult] = useState<SetupResponse | null>(null);
  const [copied, setCopied] = useState(false);

  // P8: Setup Token
  const [setupToken, setSetupToken] = useState(() => {
    // 支持 URL 参数 ?token=xxx 自动填入
    const params = new URLSearchParams(window.location.search);
    return params.get('token') || '';
  });
  const [setupTokenRequired, setSetupTokenRequired] = useState(false);

  // 获取 setup 状态判断是否需要 token
  useEffect(() => {
    fetchSetupStatus().then((status) => {
      setSetupTokenRequired(status.setup_token_required);
    }).catch(() => {});
  }, []);

  // Validation
  const validateStep1 = useCallback(() => {
    if (setupTokenRequired && !setupToken.trim()) { setAdminError('请输入 Setup Token（见服务端控制台）'); return false; }
    if (!username.trim()) { setAdminError('用户名不能为空'); return false; }
    if (password.length < 8) { setAdminError('密码至少需要 8 个字符'); return false; }
    if (!/[a-zA-Z]/.test(password) || !/\d/.test(password)) { setAdminError('密码必须同时包含字母和数字'); return false; }
    if (password !== confirmPassword) { setAdminError('两次输入的密码不一致'); return false; }
    setAdminError('');
    return true;
  }, [username, password, confirmPassword, setupToken, setupTokenRequired]);

  const validateStep2 = useCallback(() => {
    if (!serverAddr.trim()) { setAddrError('请填写服务地址'); return false; }
    try {
      const url = new URL(serverAddr);
      if (url.protocol !== 'http:' && url.protocol !== 'https:') {
        setAddrError('仅支持 http 或 https 协议');
        return false;
      }
    } catch {
      setAddrError('请输入有效的完整 URL（需包含 http:// 或 https://）');
      return false;
    }
    setAddrError('');
    return true;
  }, [serverAddr]);

  const addPort = useCallback((e?: React.FormEvent) => {
    e?.preventDefault();
    const s = parseInt(portStart);
    const e_port = portEnd ? parseInt(portEnd) : s;
    if (isNaN(s) || s < 1 || s > 65535) { setPortError('起始端口无效'); return; }
    if (isNaN(e_port) || e_port < s || e_port > 65535) { setPortError('结束端口无效'); return; }
    // 检查重复
    for (const p of ports) {
      if (s <= p.end && e_port >= p.start) { setPortError('端口范围与已有规则重叠'); return; }
    }
    setPorts([...ports, { start: s, end: e_port }]);
    setPortStart('');
    setPortEnd('');
    setPortError('');
  }, [portStart, portEnd, ports]);

  const removePort = useCallback((index: number) => {
    setPorts(ports.filter((_, i) => i !== index));
  }, [ports]);

  const addPreset = useCallback((preset: { start: number; end: number }) => {
    // 检查是否已添加
    for (const p of ports) {
      if (p.start === preset.start && p.end === preset.end) return;
    }
    setPorts([...ports, preset]);
  }, [ports]);

  const totalPorts = ports.reduce((sum, p) => sum + (p.end - p.start + 1), 0);

  const currentOrigin = window.location.origin;

  // Submit
  const handleSubmit = async () => {
    if (ports.length === 0) { setPortError('请至少添加一个端口规则'); return; }
    setLoading(true);
    setSubmitError('');
    try {
      const resp = await api.post<SetupResponse>('/api/setup/init', {
        admin: { username, password },
        server_addr: serverAddr,
        allowed_ports: ports,
        setup_token: setupToken || undefined, // P8: 携带 Setup Token
      });
      setResult(resp);
      setAuth(resp.token, resp.user);
      setStep(3); // Success page
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : '初始化失败');
    } finally {
      setLoading(false);
    }
  };

  const handleCopy = async () => {
    if (result?.agent_key?.raw_key) {
      await navigator.clipboard.writeText(result.agent_key.raw_key);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  const stepVariants = {
    initial: { opacity: 0, y: 10, filter: 'blur(4px)' },
    animate: { opacity: 1, y: 0, filter: 'blur(0px)', transition: { duration: 0.4, ease: 'easeOut' as const } },
    exit: { opacity: 0, y: -10, filter: 'blur(4px)', transition: { duration: 0.2, ease: 'easeIn' as const } },
  };

  return (
    <>
      <style>{`
        @keyframes pulse-glow {
          0%, 100% { opacity: 0.6; }
          50% { opacity: 1; }
        }
        .pulse-glow { animation: pulse-glow 3s ease-in-out infinite; }
      `}</style>

      <div className="flex h-screen w-full items-center justify-center absolute inset-0 z-50 overflow-hidden bg-background">
        {/* Canvas background */}
        <canvas
          ref={canvasRef}
          className="absolute inset-0 w-full h-full pointer-events-none"
        />

        {/* Global gradients */}
        <div className="absolute top-[-15%] left-[-10%] w-[500px] h-[500px] bg-primary/10 rounded-full blur-3xl pointer-events-none" />
        <div className="absolute bottom-[-15%] right-[-10%] w-[600px] h-[600px] bg-chart-1/10 rounded-full blur-3xl pointer-events-none" />

        <div className="w-full max-w-[420px] sm:max-w-[520px] relative z-10 px-6">
          
          {/* Header */}
          <motion.div 
            initial={{ opacity: 0, y: -20 }} 
            animate={{ opacity: 1, y: 0 }} 
            transition={{ duration: 0.6, ease: "easeOut" }}
            className="flex flex-col items-center mb-8"
          >
            <img src="/logo.svg" alt="NetsGo" className="w-12 h-12 mb-4" />
            <h1 className="text-3xl font-bold tracking-tight mb-1">初次运行向导</h1>
            <p className="text-sm text-muted-foreground">五分钟完成 NetsGo 的基础设置</p>
          </motion.div>

          {/* Minimal Step Indicator */}
          {step < 3 && (
            <motion.div 
              initial={{ opacity: 0 }} 
              animate={{ opacity: 1 }} 
              transition={{ delay: 0.2, duration: 0.4 }}
              className="flex items-center justify-between gap-2 mb-8"
            >
              {[0, 1, 2].map((i) => (
                <div key={i} className="flex-1 h-1.5 rounded-full bg-muted/50 overflow-hidden relative">
                  {i <= step && (
                    <motion.div 
                      layoutId={`indicator-${i}`}
                      initial={{ width: 0 }}
                      animate={{ width: "100%" }}
                      transition={{ duration: 0.4, ease: "easeInOut" }}
                      className={`absolute inset-0 ${i === step ? 'bg-primary pulse-glow' : 'bg-primary/50'}`}
                    />
                  )}
                </div>
              ))}
            </motion.div>
          )}

          {/* Form Container */}
          <div className="relative">
            <AnimatePresence mode="wait">
              {/* Step 1: Admin */}
              {step === 0 && (
                <motion.div
                  key="step0"
                  variants={stepVariants}
                  initial="initial"
                  animate="animate"
                  exit="exit"
                  className="space-y-5"
                >
                  <div className="flex flex-col gap-1 mb-6">
                    <h2 className="text-xl font-semibold flex items-center gap-2">
                       管理员账号
                    </h2>
                    <p className="text-sm text-muted-foreground">此账号将作为超级管理员登录控制台</p>
                  </div>

                  <form onSubmit={(e) => { e.preventDefault(); if (validateStep1()) setStep(1); }} className="space-y-4">
                    {/* P8: Setup Token 输入 */}
                    {setupTokenRequired && (
                      <div className="space-y-1.5">
                        <label className="text-sm font-medium text-foreground">Setup Token</label>
                        <div className="relative">
                          <KeyRound className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                          <Input
                            value={setupToken}
                            onChange={(e) => setSetupToken(e.target.value)}
                            placeholder="请粘贴服务端控制台输出的 Setup Token"
                            autoComplete="off"
                            className="pl-9 bg-background/50 backdrop-blur-sm font-mono text-sm"
                          />
                        </div>
                        <p className="text-[11px] text-muted-foreground">启动服务端时控制台会打印此 Token，用于防止未授权初始化</p>
                      </div>
                    )}

                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-foreground">用户名</label>
                      <div className="relative">
                        <User className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                        <Input
                          value={username}
                          onChange={(e) => setUsername(e.target.value)}
                          placeholder="admin"
                          autoComplete="username"
                          className="pl-9 bg-background/50 backdrop-blur-sm"
                        />
                      </div>
                    </div>

                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-foreground">密码</label>
                      <div className="relative">
                        <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                        <Input
                          type="password"
                          value={password}
                          onChange={(e) => setPassword(e.target.value)}
                          placeholder="至少 8 位，包含字母和数字"
                          autoComplete="new-password"
                          className="pl-9 bg-background/50 backdrop-blur-sm"
                        />
                      </div>
                    </div>

                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-foreground">确认密码</label>
                      <div className="relative">
                        <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                        <Input
                          type="password"
                          value={confirmPassword}
                          onChange={(e) => setConfirmPassword(e.target.value)}
                          placeholder="再次输入密码"
                          autoComplete="new-password"
                          className="pl-9 bg-background/50 backdrop-blur-sm"
                        />
                      </div>
                    </div>

                    {adminError && (
                      <motion.div initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: 'auto' }} className="flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2.5 rounded-lg border border-destructive/20 mt-4">
                        <AlertTriangle className="w-4 h-4 shrink-0" />
                        {adminError}
                      </motion.div>
                    )}

                    <Button type="submit" className="w-full mt-6 gap-2">
                      下一步 <ArrowRight className="w-4 h-4" />
                    </Button>
                  </form>
                </motion.div>
              )}

              {/* Step 2: Server Address */}
              {step === 1 && (
                <motion.div
                  key="step1"
                  variants={stepVariants}
                  initial="initial"
                  animate="animate"
                  exit="exit"
                  className="space-y-5"
                >
                  <div className="flex flex-col gap-1 mb-6">
                    <h2 className="text-xl font-semibold flex items-center gap-2">
                      <Server className="w-5 h-5 text-primary" />
                      Agent 接入地址
                    </h2>
                    <p className="text-sm text-muted-foreground">用于 Agent 节点建立与服务端的通信隧道</p>
                  </div>

                  <form onSubmit={(e) => { e.preventDefault(); if (validateStep2()) setStep(2); }} className="space-y-4">
                    <div className="space-y-1.5">
                      <div className="flex items-center justify-between">
                        <label className="text-sm font-medium text-foreground">公网访问 URL</label>
                        {serverAddr !== currentOrigin && (
                          <button type="button" onClick={() => { setServerAddr(currentOrigin); setAddrError(''); }} className="text-[11px] text-primary hover:underline flex items-center gap-1">
                             使用当前地址
                          </button>
                        )}
                      </div>
                      <div className="relative group">
                        <Globe className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground group-focus-within:text-primary transition-colors" />
                        <Input
                          value={serverAddr}
                          onChange={(e) => { setServerAddr(e.target.value); setAddrError(''); }}
                          placeholder="例如: https://tunnel.yourdomain.com"
                          className="pl-9 bg-background/50 backdrop-blur-sm font-mono text-sm focus-visible:ring-primary/50"
                        />
                      </div>
                    </div>

                    {/* Security Status Feedback — based on protocol × host type */}
                    {(() => {
                      if (!serverAddr) return null;
                      // 不以 http:// 或 https:// 开头的输入，不显示任何安全反馈
                      if (!/^https?:\/\//.test(serverAddr)) return null;

                      let isHttps = false;
                      let hostname = '';
                      try {
                        const url = new URL(serverAddr);
                        isHttps = url.protocol === 'https:';
                        hostname = url.hostname;
                      } catch {
                        // URL 解析失败（如端口超出范围），提示格式错误
                        return (
                          <motion.div key="invalid" initial={{ opacity: 0, y: 5 }} animate={{ opacity: 1, y: 0 }} className="rounded-xl border border-destructive/20 bg-destructive/5 p-3.5">
                            <div className="flex items-start gap-2.5">
                              <AlertTriangle className="w-4 h-4 text-destructive mt-0.5 shrink-0" />
                              <div className="space-y-0.5">
                                <p className="font-medium text-destructive text-sm">地址格式有误</p>
                                <p className="text-xs text-muted-foreground leading-relaxed">
                                  请检查 URL 是否完整且合法，例如端口号需在 0–65535 范围内。
                                </p>
                              </div>
                            </div>
                          </motion.div>
                        );
                      }

                      // 主机类型分类：IP / 合法域名 / 本地主机名
                      const isIp = /^\d{1,3}(\.\d{1,3}){3}$/.test(hostname) || hostname.startsWith('[');
                      const isLocalhost = hostname === 'localhost';
                      // 合法域名：至少包含一个点，且 TLD 至少 2 个字符（如 example.com）
                      const isDomain = !isIp && !isLocalhost && /\.[a-zA-Z]{2,}$/.test(hostname);
                      // 其余情况为单标签本地主机名（如 xx, myserver）

                      // Case 1: HTTPS + 合法域名 — 最佳实践 ✅
                      if (isHttps && isDomain) {
                        return (
                          <motion.div key="secure" initial={{ opacity: 0, y: 5 }} animate={{ opacity: 1, y: 0 }} className="rounded-xl border border-green-500/20 bg-green-500/5 p-3.5">
                            <div className="flex items-start gap-2.5">
                              <ShieldCheck className="w-4 h-4 text-green-600 dark:text-green-400 mt-0.5 shrink-0" />
                              <div className="space-y-0.5">
                                <p className="font-medium text-green-700 dark:text-green-400 text-sm">安全连接</p>
                                <p className="text-xs text-muted-foreground leading-relaxed">
                                  HTTPS + 域名是生产环境的推荐实践，可有效防止 Agent 凭证被窃听。
                                </p>
                              </div>
                            </div>
                          </motion.div>
                        );
                      }

                      // Case 2: HTTPS + IP / localhost / 本地主机名 — 加密正常，建议配域名
                      if (isHttps) {
                        return (
                          <motion.div key="https-local" initial={{ opacity: 0, y: 5 }} animate={{ opacity: 1, y: 0 }} className="rounded-xl border border-blue-500/20 bg-blue-500/5 p-3.5">
                            <div className="flex items-start gap-2.5">
                              <Shield className="w-4 h-4 text-blue-600 dark:text-blue-400 mt-0.5 shrink-0" />
                              <div className="space-y-0.5">
                                <p className="font-medium text-blue-700 dark:text-blue-400 text-sm">连接已加密</p>
                                <p className="text-xs text-muted-foreground leading-relaxed">
                                  通信已通过 TLS 加密。建议配置公网域名，便于证书管理和外部 Agent 接入。
                                </p>
                              </div>
                            </div>
                          </motion.div>
                        );
                      }

                      // Case 3: HTTP — 明确警告未加密
                      return (
                        <motion.div key="insecure" initial={{ opacity: 0, y: 5 }} animate={{ opacity: 1, y: 0 }} className="rounded-xl border border-amber-500/20 bg-amber-500/5 p-3.5">
                          <div className="flex items-start gap-2.5">
                            <AlertTriangle className="w-4 h-4 text-amber-600 dark:text-amber-500 mt-0.5 shrink-0" />
                            <div className="space-y-0.5">
                              <p className="font-medium text-amber-700 dark:text-amber-500 text-sm">连接未加密</p>
                              <p className="text-xs text-muted-foreground leading-relaxed">
                                当前使用 HTTP 明文传输{!isDomain ? '且未使用公网域名' : ''}，Agent Key 等敏感信息存在被窃听的风险。
                                建议通过反向代理配置域名并启用 HTTPS。
                                <span className="text-muted-foreground/70">（内网或测试环境可忽略）</span>
                              </p>
                            </div>
                          </div>
                        </motion.div>
                      );
                    })()}

                    {addrError && (
                      <motion.div initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: 'auto' }} className="flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2.5 rounded-lg border border-destructive/20 mt-2">
                        <AlertTriangle className="w-4 h-4 shrink-0" />
                        {addrError}
                      </motion.div>
                    )}

                    <div className="grid grid-cols-[1fr_2fr] gap-3 mt-8 pt-2">
                      <Button type="button" variant="outline" onClick={() => setStep(0)} className="gap-2 bg-background/50 backdrop-blur-sm">
                        <ArrowLeft className="w-4 h-4" /> 返回
                      </Button>
                      <Button type="submit" className="gap-2">
                        下一步 <ArrowRight className="w-4 h-4" />
                      </Button>
                    </div>
                  </form>
                </motion.div>
              )}

              {/* Step 3: Ports */}
              {step === 2 && (
                <motion.div
                  key="step2"
                  variants={stepVariants}
                  initial="initial"
                  animate="animate"
                  exit="exit"
                  className="space-y-5"
                >
                  <div className="flex flex-col gap-1 mb-6">
                    <h2 className="text-xl font-semibold flex items-center gap-2">
                       白名单端口
                    </h2>
                    <p className="text-sm text-muted-foreground">限定可以被隧道穿透或映射的公共端口范围</p>
                  </div>

                  <div className="space-y-5">
                    {/* Add port form */}
                    <form onSubmit={addPort} className="space-y-1.5">
                      <label className="text-sm font-medium text-foreground">添加端口规则</label>
                      <div className="flex items-center gap-2">
                        <Input
                          type="number"
                          placeholder="起始"
                          value={portStart}
                          onChange={(e) => setPortStart(e.target.value)}
                          min={1}
                          max={65535}
                          className="flex-1 bg-background/50 backdrop-blur-sm"
                        />
                        <span className="text-muted-foreground font-mono">-</span>
                        <Input
                          type="number"
                          placeholder="结束(选填)"
                          value={portEnd}
                          onChange={(e) => setPortEnd(e.target.value)}
                          min={1}
                          max={65535}
                          className="flex-1 bg-background/50 backdrop-blur-sm"
                        />
                        <Button type="submit" variant="secondary" size="icon" className="shrink-0 group">
                          <Plus className="w-4 h-4 transition-transform group-hover:rotate-90" />
                        </Button>
                      </div>
                    </form>

                    {/* Quick presets */}
                    <div className="space-y-2">
                      <div className="flex flex-wrap gap-2">
                        {PORT_PRESETS.map((preset) => (
                          <button
                            key={preset.label}
                            type="button"
                            onClick={() => addPreset(preset.range)}
                            className="px-3 py-1.5 text-xs font-medium bg-secondary hover:bg-secondary/80 text-secondary-foreground rounded-full transition-colors border border-border/50 backdrop-blur-sm"
                          >
                            + {preset.label} ({preset.range.start}-{preset.range.end})
                          </button>
                        ))}
                      </div>
                    </div>

                    {/* Port Error */}
                    {portError && (
                      <motion.div initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: 'auto' }} className="flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2.5 rounded-lg border border-destructive/20 mt-4">
                        <AlertTriangle className="w-4 h-4 shrink-0" />
                        {portError}
                      </motion.div>
                    )}

                    {/* Port list */}
                    {ports.length > 0 && (
                      <div className="space-y-2">
                        <div className="flex items-center justify-between">
                          <label className="text-sm font-medium text-foreground">已启用规则</label>
                          <span className="text-[11px] text-muted-foreground px-2 py-0.5 bg-muted rounded-full font-mono">
                            总计 {totalPorts.toLocaleString()} 端口
                          </span>
                        </div>
                        <div className="space-y-1.5 max-h-[160px] overflow-y-auto pr-1">
                          <AnimatePresence initial={false}>
                            {ports.map((p, i) => (
                              <motion.div
                                key={`${p.start}-${p.end}`}
                                initial={{ opacity: 0, height: 0, scale: 0.95 }}
                                animate={{ opacity: 1, height: 'auto', scale: 1 }}
                                exit={{ opacity: 0, height: 0, scale: 0.95 }}
                                className="flex items-center justify-between px-3 py-2 bg-background/50 backdrop-blur-sm rounded-lg border border-border/50 group"
                              >
                                <div className="flex items-center gap-3">
                                  <Shield className="w-3.5 h-3.5 text-primary/70" />
                                  <span className="font-mono text-sm font-medium">
                                    {p.start === p.end ? p.start : `${p.start} - ${p.end}`}
                                  </span>
                                </div>
                                <button type="button" onClick={() => removePort(i)} className="text-muted-foreground hover:text-destructive transition-colors p-1 rounded-md hover:bg-destructive/10">
                                  <X className="w-3.5 h-3.5" />
                                </button>
                              </motion.div>
                            ))}
                          </AnimatePresence>
                        </div>
                      </div>
                    )}

                    {/* Submit Error */}
                    {submitError && (
                      <motion.div initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: 'auto' }} className="flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2.5 rounded-lg border border-destructive/20 mt-4">
                        <AlertTriangle className="w-4 h-4 shrink-0" />
                        {submitError}
                      </motion.div>
                    )}

                    {/* Actions */}
                    <div className="grid grid-cols-[1fr_2fr] gap-3 mt-6 pt-2">
                      <Button type="button" variant="outline" onClick={() => setStep(1)} className="gap-2 bg-background/50 backdrop-blur-sm">
                        <ArrowLeft className="w-4 h-4" /> 返回
                      </Button>
                      <Button onClick={handleSubmit} disabled={loading || ports.length === 0} className="gap-2">
                        {loading ? (
                          <><Loader2 className="w-4 h-4 animate-spin" /> 正在应用...</>
                        ) : (
                          <><Sparkles className="w-4 h-4" /> 开启体验之旅</>
                        )}
                      </Button>
                    </div>
                  </div>
                </motion.div>
              )}

              {/* Step 4: Success */}
              {step === 3 && result && (
                <motion.div
                  key="step3"
                  variants={stepVariants}
                  initial="initial"
                  animate="animate"
                  className="space-y-6 text-center"
                >
                  <div className="flex flex-col items-center gap-4">
                    <motion.div 
                      initial={{ scale: 0 }} 
                      animate={{ scale: 1, rotate: [0, -10, 10, 0] }} 
                      transition={{ type: "spring", delay: 0.1 }}
                      className="w-16 h-16 bg-green-500/10 rounded-full flex items-center justify-center mb-2"
                    >
                      <Check className="w-8 h-8 text-green-500" />
                    </motion.div>
                    <h2 className="text-2xl font-bold">一切准备就绪！</h2>
                    <p className="text-sm text-muted-foreground -mt-2">超级管理节点已启动，基础配置完成</p>
                  </div>

                  {result.agent_key && (
                    <motion.div 
                      initial={{ opacity: 0, y: 10 }} 
                      animate={{ opacity: 1, y: 0 }} 
                      transition={{ delay: 0.3 }}
                      className="rounded-xl border border-chart-1/30 bg-chart-1/5 p-5 text-left space-y-3 backdrop-blur-sm relative overflow-hidden"
                    >
                      {/* Subtly glowing background element */}
                      <div className="absolute top-0 right-0 w-32 h-32 bg-chart-1/10 blur-2xl rounded-full" />
                      
                      <div className="relative z-10">
                        <div className="flex items-center gap-2 text-sm font-semibold text-foreground mb-3">
                          <Shield className="w-4 h-4 text-chart-1" />
                          首个 Agent Token（仅显示此一次）
                        </div>
                        <div className="flex items-center gap-2">
                          <div className="flex-1 px-3 py-2.5 bg-background/80 backdrop-blur-md rounded-lg text-sm font-mono break-all border border-border/50 shadow-inner">
                            {result.agent_key.raw_key}
                          </div>
                          <Button variant={copied ? "default" : "secondary"} size="icon" onClick={handleCopy} className={`shrink-0 h-auto self-stretch w-10 transition-colors ${copied ? 'bg-green-500 hover:bg-green-600 text-white' : ''}`}>
                            <AnimatePresence mode="wait" initial={false}>
                              {copied ? (
                                <motion.div key="check" initial={{ scale: 0.5, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} exit={{ scale: 0.5, opacity: 0 }}>
                                  <Check className="w-4 h-4" />
                                </motion.div>
                              ) : (
                                <motion.div key="copy" initial={{ scale: 0.5, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} exit={{ scale: 0.5, opacity: 0 }}>
                                  <Copy className="w-4 h-4" />
                                </motion.div>
                              )}
                            </AnimatePresence>
                          </Button>
                        </div>
                        <div className="flex items-start gap-2 text-xs text-muted-foreground mt-3 pt-3 border-t border-border/40">
                          <AlertTriangle className="w-3.5 h-3.5 text-chart-1 mt-0.5 shrink-0" />
                          <span>请立即复制并将其配置到您的内网 Agent 中，离开此页面后您只能创建新的 Token。</span>
                        </div>
                      </div>
                    </motion.div>
                  )}

                  <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} transition={{ delay: 0.5 }}>
                    <Button onClick={() => navigate({ to: '/dashboard' })} className="w-full gap-2 mt-4" size="lg">
                      进入管理面板 <ArrowRight className="w-4 h-4" />
                    </Button>
                  </motion.div>
                </motion.div>
              )}

            </AnimatePresence>
          </div>
          
        </div>
      </div>
    </>
  );
}
