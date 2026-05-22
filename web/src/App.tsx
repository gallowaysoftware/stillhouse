import { Route, Routes } from "react-router-dom";

import { HomePage } from "./pages/HomePage";
import { LoginPage } from "./pages/LoginPage";
import { MaterialsPage } from "./pages/MaterialsPage";
import { RecipeDetailPage } from "./pages/RecipeDetailPage";
import { RecipesPage } from "./pages/RecipesPage";
import { RequireAuth } from "./pages/RequireAuth";

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        path="/"
        element={
          <RequireAuth>
            <HomePage />
          </RequireAuth>
        }
      />
      <Route
        path="/materials"
        element={
          <RequireAuth>
            <MaterialsPage />
          </RequireAuth>
        }
      />
      <Route
        path="/recipes"
        element={
          <RequireAuth>
            <RecipesPage />
          </RequireAuth>
        }
      />
      <Route
        path="/recipes/:id"
        element={
          <RequireAuth>
            <RecipeDetailPage />
          </RequireAuth>
        }
      />
    </Routes>
  );
}
