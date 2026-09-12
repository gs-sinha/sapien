import { lazy, Suspense, useEffect } from 'react';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import { DaemonBanner } from './components/DaemonBanner';
import { ErrorBoundary } from './components/ErrorBoundary';
import { Nav } from './components/Nav';
import { NotFound } from './components/NotFound';
import { SearchBox } from './components/SearchBox';
import { StatusBar } from './components/StatusBar';
import { Toasts } from './components/Toasts';
import { useDaemon } from './state/daemon';
import { useEvents } from './state/events';
import { useTheme } from './state/theme';

// Every page is its own lazy chunk so the first load is the app shell only
// (nav, status bar, search) -- the bundle-size budget depends on this.
const FlowsPage = lazy(() => import('./pages/FlowsPage'));
const FlowDetailPage = lazy(() => import('./pages/FlowDetailPage'));
const RunsPage = lazy(() => import('./pages/RunsPage'));
const RunDetailPage = lazy(() => import('./pages/RunDetailPage'));
const ServicesPage = lazy(() => import('./pages/ServicesPage'));
const ServiceDetailPage = lazy(() => import('./pages/ServiceDetailPage'));
const OperationsPage = lazy(() => import('./pages/OperationsPage'));
const OperationDetailPage = lazy(() => import('./pages/OperationDetailPage'));
const ExamplesPage = lazy(() => import('./pages/ExamplesPage'));
const ExampleDetailPage = lazy(() => import('./pages/ExampleDetailPage'));
const MemoriesPage = lazy(() => import('./pages/MemoriesPage'));
const MemoryDetailPage = lazy(() => import('./pages/MemoryDetailPage'));
const EventsPage = lazy(() => import('./pages/EventsPage'));
const TryIt = lazy(() => import('./pages/TryIt'));
// Phase 7b (PLAN §34c): xterm.js and its fit addon (~75 KB gz) are only
// ever imported from AgentPage and its components (see
// state/agentTerminal.ts), so this lazy chunk is the only one that pays
// for them.
const AgentPage = lazy(() => import('./pages/AgentPage'));

function PageFallback() {
  return <div className="p-6 text-sm text-slate-400">Loading…</div>;
}

export function App() {
  useEffect(() => {
    useTheme.getState().init();
    // Record which daemon build served this tab, so a later probe can
    // tell "replaced by an upgrade" from "not running at all".
    void useDaemon.getState().probe();
    useEvents.getState().start();
    return () => useEvents.getState().stop();
  }, []);

  return (
    <BrowserRouter>
      <div className="flex h-screen flex-col bg-white text-slate-900 dark:bg-slate-950 dark:text-slate-100">
        <StatusBar />
        <DaemonBanner />
        <div className="flex flex-1 overflow-hidden">
          <Nav />
          <div className="flex flex-1 flex-col overflow-hidden">
            <div className="flex items-center gap-3 border-b border-slate-200 p-3 dark:border-slate-800">
              <SearchBox />
            </div>
            <main className="flex-1 overflow-auto">
              <ErrorBoundary>
                <Suspense fallback={<PageFallback />}>
                  <Routes>
                    <Route path="/ui" element={<Navigate to="/ui/flows" replace />} />
                    <Route path="/ui/flows" element={<FlowsPage />} />
                    <Route path="/ui/flows/:id" element={<FlowDetailPage />} />
                    <Route path="/ui/runs" element={<RunsPage />} />
                    <Route path="/ui/runs/:id" element={<RunDetailPage />} />
                    <Route path="/ui/services" element={<ServicesPage />} />
                    <Route path="/ui/services/:name" element={<ServiceDetailPage />} />
                    <Route path="/ui/operations" element={<OperationsPage />} />
                    <Route path="/ui/operations/:id" element={<OperationDetailPage />} />
                    <Route path="/ui/examples" element={<ExamplesPage />} />
                    <Route path="/ui/examples/:id" element={<ExampleDetailPage />} />
                    <Route path="/ui/memories" element={<MemoriesPage />} />
                    <Route path="/ui/memories/:id" element={<MemoryDetailPage />} />
                    <Route path="/ui/events" element={<EventsPage />} />
                    <Route path="/ui/try/:operationId" element={<TryIt />} />
                    <Route path="/ui/agent" element={<AgentPage />} />
                    <Route path="*" element={<NotFound />} />
                  </Routes>
                </Suspense>
              </ErrorBoundary>
            </main>
          </div>
        </div>
        <Toasts />
      </div>
    </BrowserRouter>
  );
}
