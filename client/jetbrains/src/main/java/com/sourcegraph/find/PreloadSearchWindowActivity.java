package com.sourcegraph.find;

import com.intellij.openapi.project.Project;
import com.intellij.openapi.startup.StartupActivity;
import org.jetbrains.annotations.NotNull;

public class PreloadSearchWindowActivity implements StartupActivity {

    @Override
    public void runActivity(@NotNull Project project) {
        SearchWindowService service = project.getService(SearchWindowService.class);
        service.getSearchWindow();
    }
}
