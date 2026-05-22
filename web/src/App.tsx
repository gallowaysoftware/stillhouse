import { Route, Routes } from "react-router-dom";

import { BarrelDetailPage } from "./pages/BarrelDetailPage";
import { BarrelsPage } from "./pages/BarrelsPage";
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
import { MaterialsPage } from "./pages/MaterialsPage";
import { RecipeDetailPage } from "./pages/RecipeDetailPage";
import { RecipesPage } from "./pages/RecipesPage";
import { RequireAuth } from "./pages/RequireAuth";

function Guarded({ children }: { children: React.ReactNode }) {
  return <RequireAuth>{children}</RequireAuth>;
}

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/" element={<Guarded><HomePage /></Guarded>} />
      <Route path="/materials" element={<Guarded><MaterialsPage /></Guarded>} />
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
    </Routes>
  );
}
