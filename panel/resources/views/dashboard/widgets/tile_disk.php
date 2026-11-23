<?php
/** @var \NimVor\View\View $this */
echo $this->partial('dashboard/tile', ['key' => 'disk', 'label' => 'Disk (/)', 'icon' => 'hdd', 'series' => 'disk']);
