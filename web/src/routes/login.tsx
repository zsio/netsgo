import { useState, useRef, useEffect, useCallback } from 'react';
import { createRoute, useNavigate } from '@tanstack/react-router';
import { rootRoute } from './__root';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useAuthStore } from '@/stores/auth-store';
import { api } from '@/lib/api';
import type { LoginResponse } from '@/types';
import { requireLoginPage } from '@/lib/auth';
import { User, Lock, AlertTriangle, ArrowRight, Loader2, Github } from 'lucide-react';

export const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  beforeLoad: requireLoginPage,
  component: LoginPage,
});

/* ─── Particle Network Animation ─── */

interface Particle {
  x: number;
  y: number;
  vx: number;
  vy: number;
  radius: number;
  color: string;
}

function useParticleCanvas(canvasRef: React.RefObject<HTMLCanvasElement | null>) {
  const particles = useRef<Particle[]>([]);
  const animFrameId = useRef(0);

  const initParticles = useCallback((w: number, h: number) => {
    const count = Math.floor((w * h) / 12000); // adaptive density
    const arr: Particle[] = [];
    for (let i = 0; i < count; i++) {
      const isAccent = Math.random() < 0.3;
      arr.push({
        x: Math.random() * w,
        y: Math.random() * h,
        vx: (Math.random() - 0.5) * 0.4,
        vy: (Math.random() - 0.5) * 0.4,
        radius: Math.random() * 2 + 1,
        color: isAccent
          ? 'rgba(251, 133, 4, 0.7)'   // brand orange
          : 'rgba(160, 160, 170, 0.35)', // neutral gray
      });
    }
    particles.current = arr;
  }, []);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    const resize = () => {
      const dpr = window.devicePixelRatio || 1;
      const rect = canvas.getBoundingClientRect();
      canvas.width = rect.width * dpr;
      canvas.height = rect.height * dpr;
      ctx.scale(dpr, dpr);
      initParticles(rect.width, rect.height);
    };

    resize();
    window.addEventListener('resize', resize);

    const draw = () => {
      const rect = canvas.getBoundingClientRect();
      const w = rect.width;
      const h = rect.height;
      ctx.clearRect(0, 0, w, h);

      const pts = particles.current;

      // Update positions
      for (const p of pts) {
        p.x += p.vx;
        p.y += p.vy;
        if (p.x < 0 || p.x > w) p.vx *= -1;
        if (p.y < 0 || p.y > h) p.vy *= -1;
      }

      // Draw connections
      const maxDist = 120;
      for (let i = 0; i < pts.length; i++) {
        for (let j = i + 1; j < pts.length; j++) {
          const dx = pts[i].x - pts[j].x;
          const dy = pts[i].y - pts[j].y;
          const dist = Math.sqrt(dx * dx + dy * dy);
          if (dist < maxDist) {
            const alpha = (1 - dist / maxDist) * 0.15;
            ctx.strokeStyle = `rgba(251, 133, 4, ${alpha})`;
            ctx.lineWidth = 0.8;
            ctx.beginPath();
            ctx.moveTo(pts[i].x, pts[i].y);
            ctx.lineTo(pts[j].x, pts[j].y);
            ctx.stroke();
          }
        }
      }

      // Draw particles
      for (const p of pts) {
        ctx.beginPath();
        ctx.arc(p.x, p.y, p.radius, 0, Math.PI * 2);
        ctx.fillStyle = p.color;
        ctx.fill();
      }

      animFrameId.current = requestAnimationFrame(draw);
    };

    animFrameId.current = requestAnimationFrame(draw);

    return () => {
      window.removeEventListener('resize', resize);
      cancelAnimationFrame(animFrameId.current);
    };
  }, [canvasRef, initParticles]);
}

/* ─── Login Page ─── */

function LoginPage() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const setAuth = useAuthStore((s) => s.setAuth);
  const navigate = useNavigate();
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useParticleCanvas(canvasRef);

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);

    try {
      const resp = await api.post<LoginResponse>('/api/auth/login', { username, password });
      setAuth(resp.token, resp.user);
      navigate({ to: '/dashboard' });
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败，请检查用户名和密码');
    } finally {
      setLoading(false);
    }
  };

  return (
    <>
      <style>{`
        @keyframes login-float {
          0%, 100% { transform: translateY(0); }
          50% { transform: translateY(-8px); }
        }
        @keyframes login-fade-in {
          from { opacity: 0; transform: translateY(8px); }
          to { opacity: 1; transform: translateY(0); }
        }
        @keyframes login-pulse-glow {
          0%, 100% { opacity: 0.6; }
          50% { opacity: 1; }
        }
        .login-float { animation: login-float 6s ease-in-out infinite; }
        .login-fade-in { animation: login-fade-in 0.6s ease-out both; }
        .login-fade-in-delay { animation: login-fade-in 0.6s 0.15s ease-out both; }
        .login-pulse-glow { animation: login-pulse-glow 3s ease-in-out infinite; }
      `}</style>

      <div className="flex h-screen w-full absolute inset-0 z-50 bg-background">
        {/* ─── Left Panel: Brand + Particle Network ─── */}
        <div className="hidden md:flex md:w-[45%] lg:w-1/2 relative overflow-hidden bg-muted/30 border-r border-border/40 items-center justify-center">
          {/* Canvas particle background */}
          <canvas
            ref={canvasRef}
            className="absolute inset-0 w-full h-full"
          />

          {/* Decorative gradient blobs */}
          <div className="absolute top-[-10%] right-[-5%] w-[400px] h-[400px] bg-primary/10 rounded-full blur-3xl pointer-events-none" />
          <div className="absolute bottom-[-10%] left-[-5%] w-[350px] h-[350px] bg-chart-1/10 rounded-full blur-3xl pointer-events-none" />

          {/* Brand content */}
          <div className="relative z-10 flex flex-col items-center gap-6 px-8 text-center login-fade-in">
            <img src="/logo.svg" alt="NetsGo" className="w-16 h-16" />
            <div className="space-y-2">
              <h2 className="text-3xl font-bold tracking-tight">NetsGo</h2>
              <p className="text-muted-foreground text-sm max-w-[260px]">
                安全、高效的网络隧道管理平台
              </p>
            </div>
            {/* Decorative dots */}
            <div className="flex items-center gap-2 mt-4">
              <div className="w-2 h-2 rounded-full bg-primary/60 login-pulse-glow" />
              <div className="w-8 h-px bg-border" />
              <div className="w-2 h-2 rounded-full bg-primary/40" />
              <div className="w-8 h-px bg-border" />
              <div className="w-2 h-2 rounded-full bg-primary/20" />
            </div>
          </div>
        </div>

        {/* ─── Right Panel: Login Form ─── */}
        <div className="flex-1 flex items-center justify-center relative overflow-hidden px-6">
          {/* Mobile-only: subtle background decoration */}
          <div className="absolute top-0 left-1/2 -translate-x-1/2 w-[500px] h-[300px] bg-primary/5 rounded-full blur-3xl pointer-events-none md:hidden" />

          <div className="w-full max-w-[340px] relative z-10">
            {/* Logo + Header */}
            <div className="flex flex-col items-start gap-2 mb-10 login-fade-in">
              <img src="/logo.svg" alt="NetsGo" className="w-10 h-10 mb-2 md:hidden" />
              <h1 className="text-3xl font-bold tracking-tight">欢迎回来</h1>
              <p className="text-sm text-muted-foreground">请输入 NetsGo 管理员账号和密码</p>
            </div>

            {/* Form — no card at all, raw inputs on the background */}
            <div className="login-fade-in-delay">
              <form onSubmit={handleLogin} className="flex flex-col gap-5">
                {/* Username */}
                <div className="space-y-2">
                  <label className="text-sm font-medium text-foreground">
                    用户名
                  </label>
                  <div className="relative">
                    <User className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                    <Input
                      type="text"
                      placeholder="请输入用户名"
                      value={username}
                      onChange={(e) => setUsername(e.target.value)}
                      disabled={loading}
                      autoComplete="username"
                      required
                      className="pl-9"
                    />
                  </div>
                </div>

                {/* Password */}
                <div className="space-y-2">
                  <label className="text-sm font-medium text-foreground">
                    密码
                  </label>
                  <div className="relative">
                    <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                    <Input
                      type="password"
                      placeholder="请输入密码"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      disabled={loading}
                      autoComplete="current-password"
                      required
                      className="pl-9"
                    />
                  </div>
                </div>

                {/* Error message */}
                {error && (
                  <div className="login-fade-in flex items-center gap-2 text-sm text-destructive bg-destructive/10 px-3 py-2.5 rounded-lg border border-destructive/20">
                    <AlertTriangle className="w-4 h-4 shrink-0" />
                    {error}
                  </div>
                )}

                {/* Submit button */}
                <Button
                  type="submit"
                  className="w-full mt-4 gap-2 h-10"
                  disabled={loading}
                >
                  {loading ? (
                    <>
                      <Loader2 className="w-4 h-4 animate-spin" />
                      登录中...
                    </>
                  ) : (
                    <>
                      登 录
                      <ArrowRight className="w-4 h-4" />
                    </>
                  )}
                </Button>
              </form>
            </div>

            {/* Footer */}
            <div className="flex flex-col gap-2 text-left mt-8 login-fade-in-delay">
              <div className="flex items-center gap-1.5 text-xs text-muted-foreground/80">
                <a
                  href="https://github.com/zsio/netsgo"
                  target="_blank"
                  rel="noreferrer"
                  className="flex items-center gap-1 font-medium hover:text-foreground transition-colors"
                >
                  <Github className="w-3.5 h-3.5" />
                  <span>NetsGo</span>
                </a>
                <span>· 网络隧道管理平台</span>
              </div>
              <p className="text-xs text-muted-foreground/50">不收集任何数据，所以不需要隐私协议。</p>
            </div>
          </div>
        </div>
      </div>
    </>
  );
}
