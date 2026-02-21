# Subscription Theme Background Images

This directory contains background pattern images for subscription page themes.

## Image Files

Place the following image files in this directory:

- `rainbow.png` - Rainbow theme pattern (unicorns, rainbows, stars with rainbow colors)
- `coffee.png` - Coffee theme pattern (coffee cups, beans, pastries in brown tones)
- `banana.png` - Banana theme pattern (bananas, smoothies, ice cream in yellow tones)
- `sunset.png` - Sunset theme pattern (suns, stars, celestial elements in orange/gold tones)

## Usage

These images will be automatically used as background for the subscription card when the corresponding theme is selected, unless a custom background URL is specified in settings.

**How it works:**
1. When a theme is selected (Rainbow, Coffee, Banana, or Sunset), the system automatically looks for the corresponding image file in this directory
2. If the image file exists, it will be used as the background
3. If a custom background URL is set in settings, it will override the theme image
4. If no theme is selected or image file is missing, the theme gradient will be used instead

## File Format

- **Recommended format:** PNG with transparency
- **Recommended size:** 800x600px or larger (will be scaled with `background-size: cover`)
- **Pattern:** Should be seamless/repeating for best results
- **File naming:** Must match exactly: `rainbow.png`, `coffee.png`, `banana.png`, `sunset.png`

## Adding Your Images

1. Copy your pattern images to this directory (`web/assets/images/subscription-themes/`)
2. Name them exactly as listed above
3. Restart the panel or rebuild if using embedded assets
4. The images will be automatically served at: `/assets/images/subscription-themes/{theme}.png`

## Note for Production Builds

If you're building a production binary with embedded assets, make sure to rebuild after adding images so they are included in the embedded filesystem.
