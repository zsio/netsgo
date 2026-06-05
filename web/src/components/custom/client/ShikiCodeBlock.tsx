import { useEffect, useId, useMemo, useState } from 'react';

import { ScrollArea, ScrollBar } from '@/components/ui/scroll-area';

type CodeLanguage = 'bash' | 'yaml';
type ColorMode = 'light' | 'dark';

interface ShikiCodeBlockProps {
  code: string;
  language: CodeLanguage;
}

const LIGHT_THEME = 'github-light';
const DARK_THEME = 'github-dark';

type ShikiHighlighter = {
  codeToHtml: (code: string, options: { lang: CodeLanguage; theme: string }) => string;
};

let highlighterPromise: Promise<ShikiHighlighter> | null = null;

function getHighlighter() {
  highlighterPromise ??= Promise.all([
    import('@shikijs/core'),
    import('@shikijs/engine-javascript'),
    import('@shikijs/langs/bash'),
    import('@shikijs/langs/yaml'),
    import('@shikijs/themes/github-light'),
    import('@shikijs/themes/github-dark'),
  ])
    .then(([
      { createHighlighterCore },
      { createJavaScriptRegexEngine },
      bash,
      yaml,
      githubLight,
      githubDark,
    ]) => createHighlighterCore({
      engine: createJavaScriptRegexEngine(),
      langs: [bash.default, yaml.default],
      themes: [githubLight.default, githubDark.default],
    }));

  return highlighterPromise;
}

function getColorMode(): ColorMode {
  if (typeof document !== 'undefined' && document.documentElement.classList.contains('dark')) {
    return 'dark';
  }

  return 'light';
}

function useColorMode() {
  const [mode, setMode] = useState<ColorMode>(getColorMode);

  useEffect(() => {
    const syncMode = () => setMode(getColorMode());
    const observer = new MutationObserver(syncMode);

    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class'],
    });

    syncMode();

    return () => {
      observer.disconnect();
    };
  }, []);

  return mode;
}

export function ShikiCodeBlock({ code, language }: ShikiCodeBlockProps) {
  const mode = useColorMode();
  const rawId = useId();
  const blockClassName = useMemo(
    () => `shiki-code-block-${rawId.replace(/[^a-zA-Z0-9_-]/g, '')}`,
    [rawId],
  );
  const [html, setHtml] = useState('');

  useEffect(() => {
    let cancelled = false;

    getHighlighter()
      .then((highlighter) => {
        const nextHtml = highlighter.codeToHtml(code, {
          lang: language,
          theme: mode === 'dark' ? DARK_THEME : LIGHT_THEME,
        });

        if (!cancelled) {
          setHtml(nextHtml);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setHtml('');
        }
      });

    return () => {
      cancelled = true;
    };
  }, [code, language, mode]);

  return (
    <ScrollArea className="h-[clamp(8rem,calc(100vh-28rem),14rem)]">
      <div className={blockClassName}>
        <style>
          {`
            .${blockClassName} .shiki {
              min-width: max-content;
              margin: 0;
              padding: 0.625rem 3rem 0.625rem 0;
              overflow: visible;
              background-color: transparent !important;
              font-family: var(--font-mono);
              font-size: 0.75rem;
              line-height: 1.25rem;
            }

            .${blockClassName} .shiki code {
              display: block;
              counter-reset: line;
            }

            .${blockClassName} .shiki .line {
              position: relative;
              display: inline-block;
              min-width: 100%;
              min-height: 1.25rem;
              padding-left: 3rem;
              padding-right: 0.75rem;
              box-sizing: border-box;
            }

            .${blockClassName} .shiki .line::before {
              position: absolute;
              left: 0;
              width: 2rem;
              color: var(--muted-foreground);
              content: counter(line);
              counter-increment: line;
              font-variant-numeric: tabular-nums;
              opacity: 0.55;
              text-align: right;
              user-select: none;
            }
          `}
        </style>
        {html ? (
          <div dangerouslySetInnerHTML={{ __html: html }} />
        ) : (
          <pre className="m-0 min-w-max p-3 pr-12 pl-12 font-mono text-xs leading-5 text-foreground">
            <code>{code}</code>
          </pre>
        )}
      </div>
      <ScrollBar orientation="horizontal" />
    </ScrollArea>
  );
}
