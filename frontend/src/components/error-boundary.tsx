import React from "react";
import { Button } from "@/components/ui/button";

interface Props {
  children: React.ReactNode;
}

interface State {
  error: Error | null;
}

/**
 * 顶层错误边界:任何页面渲染抛异常时兜住,给出可操作的降级界面,
 * 而不是整窗白屏(桌面应用里用户没有"开 devtools 看报错"的习惯,
 * 白屏等于应用坏了,且没有任何线索)。
 *
 * 只兜渲染期错误:事件回调里的异常(点击处理器等)React 不冒泡到
 * 边界,那些路径各自有 reportError 兜底。
 */
export class ErrorBoundary extends React.Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    // 留痕:排查要靠它。不上报后端(没有上报通道),控制台是唯一出口。
    console.error("页面渲染异常(已由错误边界兜住)", error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div
          role="alert"
          className="flex h-full flex-col items-center justify-center gap-4 p-8 text-center"
        >
          <div className="text-lg font-semibold">页面出错了</div>
          <p className="max-w-md text-sm text-muted-foreground">
            界面渲染时发生异常,其余功能不受影响。
            错误详情已记录在控制台;重试或刷新通常可以恢复。
          </p>
          <pre className="max-h-40 max-w-lg overflow-auto rounded-md bg-muted/40 p-3 text-left text-xs text-muted-foreground">
            {this.state.error.message}
          </pre>
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => this.setState({ error: null })}>
              重试
            </Button>
            <Button onClick={() => window.location.reload()}>刷新应用</Button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
