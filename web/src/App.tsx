import { Link, Route, Routes } from 'react-router-dom';
import { Shell, Toasts } from './components/Shell';
import { Card, Empty, Skeleton } from './components/ui';
import { useLive } from './lib/live';
import { workloadName } from './lib/vocab';
import { Audit } from './pages/Audit';
import { Clusters } from './pages/Clusters';
import { DemoLab } from './pages/DemoLab';
import { IncidentDetail, useIncident } from './pages/IncidentDetail';
import { Incidents } from './pages/Incidents';
import { Login } from './pages/Login';
import { Overview } from './pages/Overview';
import { Pods } from './pages/Pods';
import { Settings } from './pages/Settings';

function IncidentShell() {
  const i = useIncident();
  const crumb = <><Link to="/incidents" className="text-fg-2 hover:text-fg">Incidents</Link> / {i?.id ?? ''}</>;
  return <Shell title={i ? workloadName(i) : 'Incident'} crumb={crumb} />;
}

export function App() {
  const { me } = useLive();
  if (!me) return <div className="p-8"><Skeleton className="h-40 max-w-xl" /></div>;
  if (me.authEnabled && !me.user) return <><Login /><Toasts /></>;
  return (
    <>
      <Routes>
        <Route element={<Shell title="Overview" />}><Route index element={<Overview />} /></Route>
        <Route element={<Shell title="Incidents" />}><Route path="incidents" element={<Incidents />} /></Route>
        <Route element={<IncidentShell />}><Route path="incidents/:id" element={<IncidentDetail />} /></Route>
        <Route element={<Shell title="Clusters" />}><Route path="clusters" element={<Clusters />} /></Route>
        <Route element={<Shell title="Pods" />}><Route path="pods" element={<Pods />} /></Route>
        <Route element={<Shell title="Audit log" />}><Route path="audit" element={<Audit />} /></Route>
        <Route element={<Shell title="Settings" />}><Route path="settings" element={<Settings />} /></Route>
        <Route element={<Shell title="Demo lab" />}><Route path="demo" element={<DemoLab />} /></Route>
        <Route element={<Shell title="Not found" />}><Route path="*" element={<Card><Empty title="This page does not exist"><Link to="/" className="text-accent">Go to the overview</Link></Empty></Card>} /></Route>
      </Routes>
      <Toasts />
    </>
  );
}
