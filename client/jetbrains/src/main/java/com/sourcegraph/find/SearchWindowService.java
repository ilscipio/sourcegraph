package com.sourcegraph.find;

import com.intellij.openapi.project.Project;

public class SearchWindowService {
    private final SearchWindow searchWindow;

    public SearchWindowService(Project project) {
        searchWindow = new SearchWindow(project);
    }


    public SearchWindow getSearchWindow() {
        return this.searchWindow;
    }
}
