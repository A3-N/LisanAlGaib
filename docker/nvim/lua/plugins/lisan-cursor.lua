return {
  {
    "gen740/SmoothCursor.nvim",
    commit = "12518b284e1e3f7c6c703b346815968e1620bee2",
    event = "VeryLazy",
    opts = {
      type = "default",
      autostart = true,
      threshold = 3,
      disable_float_win = true,
      disabled_filetypes = { "TelescopePrompt", "NvimTree", "lazy", "mason" },
    },
  },
  {
    "sphamba/smear-cursor.nvim",
    commit = "9e9378d6ee34bb3782e0e8c63d9ec8ca618b479b",
    event = "VeryLazy",
    cond = vim.g.neovide == nil,
    opts = {
      hide_target_hack = true,
      cursor_color = "none",
      smear_terminal_mode = false,
    },
    specs = {
      {
        "nvim-mini/mini.animate",
        optional = true,
        opts = {
          cursor = { enable = false },
        },
      },
    },
  },
}
