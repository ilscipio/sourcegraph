package com.sourcegraph.listener;

import com.intellij.ide.ui.LafManager;
import com.intellij.ide.ui.LafManagerListener;
import org.jetbrains.annotations.NotNull;

public class ThemeChangeListener implements LafManagerListener {

    @Override
    public void lookAndFeelChanged(@NotNull LafManager lafManager) {
        System.out.println("YAY");
    }
}
