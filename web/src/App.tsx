import { Route, Routes } from "react-router-dom";

import { AuditPage } from "./pages/AuditPage";
import { B266Page } from "./pages/B266Page";
import { BarrelDetailPage } from "./pages/BarrelDetailPage";
import { BarrelsPage } from "./pages/BarrelsPage";
import { BottlingPage } from "./pages/BottlingPage";
import { BulkContainerDetailPage } from "./pages/BulkContainerDetailPage";
import { BulkPage } from "./pages/BulkPage";
import { DistillationDetailPage } from "./pages/DistillationDetailPage";
import { DistillationsPage } from "./pages/DistillationsPage";
import { FermentationDetailPage } from "./pages/FermentationDetailPage";
import { FermentationsPage } from "./pages/FermentationsPage";
import { HomePage } from "./pages/HomePage";
import { LoginPage } from "./pages/LoginPage";
import { MashDetailPage } from "./pages/MashDetailPage";
import { MashesPage } from "./pages/MashesPage";
import { MaterialDetailPage } from "./pages/MaterialDetailPage";
import { MaterialsPage } from "./pages/MaterialsPage";
import { PricingPage } from "./pages/PricingPage";
import { ProductsPage } from "./pages/ProductsPage";
import { RecipeDetailPage } from "./pages/RecipeDetailPage";
import { RecipesPage } from "./pages/RecipesPage";
import { RemovalsPage } from "./pages/RemovalsPage";
import { RequireAuth } from "./pages/RequireAuth";
import { SettingsPage } from "./pages/SettingsPage";
import { StampsPage } from "./pages/StampsPage";

function Guarded({ children }: { children: React.ReactNode }) {
  return <RequireAuth>{children}</RequireAuth>;
}

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/" element={<Guarded><HomePage /></Guarded>} />
      <Route path="/materials" element={<Guarded><MaterialsPage /></Guarded>} />
      <Route path="/materials/:id" element={<Guarded><MaterialDetailPage /></Guarded>} />
      <Route path="/recipes" element={<Guarded><RecipesPage /></Guarded>} />
      <Route path="/recipes/:id" element={<Guarded><RecipeDetailPage /></Guarded>} />
      <Route path="/mashes" element={<Guarded><MashesPage /></Guarded>} />
      <Route path="/mashes/:id" element={<Guarded><MashDetailPage /></Guarded>} />
      <Route path="/fermentations" element={<Guarded><FermentationsPage /></Guarded>} />
      <Route path="/fermentations/:id" element={<Guarded><FermentationDetailPage /></Guarded>} />
      <Route path="/distillations" element={<Guarded><DistillationsPage /></Guarded>} />
      <Route path="/distillations/:id" element={<Guarded><DistillationDetailPage /></Guarded>} />
      <Route path="/bulk" element={<Guarded><BulkPage /></Guarded>} />
      <Route path="/bulk/:id" element={<Guarded><BulkContainerDetailPage /></Guarded>} />
      <Route path="/barrels" element={<Guarded><BarrelsPage /></Guarded>} />
      <Route path="/barrels/:id" element={<Guarded><BarrelDetailPage /></Guarded>} />
      <Route path="/products" element={<Guarded><ProductsPage /></Guarded>} />
      <Route path="/stamps" element={<Guarded><StampsPage /></Guarded>} />
      <Route path="/bottling" element={<Guarded><BottlingPage /></Guarded>} />
      <Route path="/removals" element={<Guarded><RemovalsPage /></Guarded>} />
      <Route path="/b266" element={<Guarded><B266Page /></Guarded>} />
      <Route path="/audit" element={<Guarded><AuditPage /></Guarded>} />
      <Route path="/pricing" element={<Guarded><PricingPage /></Guarded>} />
      <Route path="/settings" element={<Guarded><SettingsPage /></Guarded>} />
    </Routes>
  );
}
