import { Component, type ReactNode } from "react";
import { TriangleAlert } from "lucide-react";
import { t } from "../lib/i18n";

/** Keeps a bug in one view from blanking the whole window (terminals keep running). */
export class ErrorBoundary extends Component<{ children: ReactNode; resetKey?: string }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidUpdate(prev: { resetKey?: string }) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null });
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="page">
        <div className="callout crit" style={{ marginTop: 24 }}>
          <TriangleAlert size={16} />
          <div className="grow">
            <strong>{t("err.generic")}</strong>
            <div className="mono muted selectable" style={{ marginTop: 4 }}>
              {this.state.error.message}
            </div>
          </div>
          <button className="btn sm" onClick={() => location.reload()}>
            {t("common.retry")}
          </button>
        </div>
      </div>
    );
  }
}
